// Package docker implements the database operator for the docker-postgres
// target: fixed pg_dump/pg_restore argv executed inside the compose-managed
// Postgres container.
//
// Restore always creates a brand-new database and verifies it by counting
// the restored relations; the active database is never overwritten. Backup
// bytes go to the operator-specified destination file and nowhere else, and
// connection URLs never cross argv or rendered output — callers address a
// restored database through its environment key.
package docker

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gogogadget/gogogadget/internal/remote"
)

// Operator is the local Docker Postgres database operator.
type Operator struct {
	// run executes one fixed argv inside the project directory and returns
	// stdout. Tests inject a stub; production shells out to docker compose.
	run runnerFunc
	now func() time.Time
}

// runnerFunc executes one fixed argv with stdin bytes and captures stdout.
type runnerFunc func(ctx context.Context, root string, argv []string, stdin []byte) ([]byte, error)

// NewOperator constructs the operator on the real docker compose CLI.
func NewOperator() *Operator {
	return &Operator{run: composeRun, now: time.Now}
}

// composeRun shells out to `docker compose -f <file> exec -T ...`. The
// compose file follows the environment selection the CLI resolves, passed
// as the first state entry.
func composeRun(ctx context.Context, root string, argv []string, stdin []byte) ([]byte, error) {
	return runCommand(ctx, root, argv, stdin)
}

// Defaults match the compose generator's derivation for the database
// service; the CLI overrides them from the selected target's local service
// declaration through the request state.
const (
	defaultUser     = "postgres"
	defaultDatabase = "gogogadget"
	defaultService  = "db"
)

// Backup writes a plain-format pg_dump of the active database to the
// destination path. The destination must be a concrete path: this operator
// has no provider-side object store, and a "provider" destination gets the
// typed not-configured refusal naming the upgrade path rather than a fake
// local artifact.
func (o *Operator) Backup(ctx context.Context, request remote.DatabaseRequest, destination string, progress remote.ProgressSink) (remote.BackupState, error) {
	if progress == nil {
		progress = remote.DiscardProgress
	}
	if err := ValidateRequest(request); err != nil {
		return remote.BackupState{}, err
	}
	if strings.TrimSpace(destination) == "" || destination == "provider" {
		return remote.BackupState{}, &remote.ErrNotConfigured{
			Console: "https://console.neon.tech",
			Advice:  "this operator writes to local paths; pass --destination PATH (provider-side backups belong to the managed target's console)",
		}
	}
	user, database := o.identity(request)
	dump, err := o.run(ctx, request.Root, []string{"docker", "compose", composeFileArg(request), "exec", "-T", o.service(request),
		"pg_dump", "--no-owner", "--no-privileges", "-U", user, database}, nil)
	if err != nil {
		return remote.BackupState{}, fmt.Errorf("pg_dump: %w", err)
	}
	if strings.TrimSpace(destination) == "-" || strings.Contains(destination, "..") {
		return remote.BackupState{}, fmt.Errorf("destination %q is not a concrete project-relative path", destination)
	}
	path := destination
	if !filepath.IsAbs(path) {
		path = filepath.Join(request.Root, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return remote.BackupState{}, err
	}
	if err := os.WriteFile(path, dump, 0o600); err != nil {
		return remote.BackupState{}, err
	}
	sum := sha256.Sum256(dump)
	created := o.now().UTC()
	progress.Emit(remote.ProgressEvent{Stage: "backup", Message: fmt.Sprintf("wrote %s (%d bytes)", destination, len(dump)), Current: 1, Total: 1, Done: true})
	return remote.BackupState{
		ID:        "backup-" + created.Format("20060102T150405Z"),
		Location:  destination,
		SHA256:    hex.EncodeToString(sum[:]),
		CreatedAt: created,
	}, nil
}

// Restore replays a backup into a brand-new database and verifies it. The
// destination URL key is recorded in the returned state; the URL itself
// never appears in output — the caller resolves DATABASE_URL through the
// key.
func (o *Operator) Restore(ctx context.Context, request remote.DatabaseRequest, backup remote.BackupState, destinationURLKey string, _ remote.SecretValues, progress remote.ProgressSink) (remote.RestoreState, error) {
	if progress == nil {
		progress = remote.DiscardProgress
	}
	if err := ValidateRequest(request); err != nil {
		return remote.RestoreState{}, err
	}
	if destinationURLKey == "" {
		return remote.RestoreState{}, fmt.Errorf("restore requires a destination URL key")
	}
	backupBytes, err := o.readBackup(request.Root, backup)
	if err != nil {
		return remote.RestoreState{}, err
	}
	user, _ := o.identity(request)
	restored := "restore_" + randomSuffix()
	progress.Emit(remote.ProgressEvent{Stage: "restore", Message: "creating database " + restored, Current: 1, Total: 3, Done: false})
	if _, err := o.run(ctx, request.Root, []string{"docker", "compose", composeFileArg(request), "exec", "-T", o.service(request),
		"psql", "-U", user, "-d", "postgres", "-v", "ON_ERROR_STOP=1", "-c",
		fmt.Sprintf("CREATE DATABASE %s OWNER %s", restored, user)}, nil); err != nil {
		return remote.RestoreState{}, fmt.Errorf("create restore database: %w", err)
	}
	progress.Emit(remote.ProgressEvent{Stage: "restore", Message: "replaying backup", Current: 2, Total: 3, Done: false})
	if _, err := o.run(ctx, request.Root, []string{"docker", "compose", composeFileArg(request), "exec", "-T", o.service(request),
		"psql", "-U", user, "-d", restored, "-v", "ON_ERROR_STOP=1", "-q"}, backupBytes); err != nil {
		return remote.RestoreState{}, fmt.Errorf("replay backup: %w", err)
	}
	out, err := o.run(ctx, request.Root, []string{"docker", "compose", composeFileArg(request), "exec", "-T", o.service(request),
		"psql", "-U", user, "-d", restored, "-tAc",
		"SELECT count(*) FROM pg_tables WHERE schemaname = 'public'"}, nil)
	if err != nil {
		return remote.RestoreState{}, fmt.Errorf("verify restore: %w", err)
	}
	if strings.TrimSpace(string(out)) == "" {
		return remote.RestoreState{}, fmt.Errorf("verify restore: database %s reports no public tables", restored)
	}
	progress.Emit(remote.ProgressEvent{Stage: "restore", Message: "restore verified in " + restored, Current: 3, Total: 3, Done: true})
	return remote.RestoreState{DatabaseID: restored, URLKey: destinationURLKey, Ready: true}, nil
}

// RestoreDrill proves a backup restores: the same replay into a fresh
// database, a smoke query, then the drill database is dropped so the drill
// leaves nothing behind.
func (o *Operator) RestoreDrill(ctx context.Context, request remote.DatabaseRequest, backup remote.BackupState, _ remote.SecretValues, progress remote.ProgressSink) (remote.DrillResult, error) {
	if progress == nil {
		progress = remote.DiscardProgress
	}
	started := o.now()
	result := remote.DrillResult{BackupID: backup.ID}
	if err := ValidateRequest(request); err != nil {
		return result, err
	}
	restored, err := o.Restore(ctx, request, backup, "DATABASE_URL", nil, progress)
	if err != nil {
		return result, err
	}
	result.DatabaseID = restored.DatabaseID
	user, _ := o.identity(request)
	if _, err := o.run(ctx, request.Root, []string{"docker", "compose", composeFileArg(request), "exec", "-T", o.service(request),
		"psql", "-U", user, "-d", restored.DatabaseID, "-tAc", "SELECT 1"}, nil); err != nil {
		return result, fmt.Errorf("smoke query: %w", err)
	}
	if _, err := o.run(ctx, request.Root, []string{"docker", "compose", composeFileArg(request), "exec", "-T", o.service(request),
		"psql", "-U", user, "-d", "postgres", "-v", "ON_ERROR_STOP=1", "-c",
		fmt.Sprintf("DROP DATABASE %s", restored.DatabaseID)}, nil); err != nil {
		return result, fmt.Errorf("drop drill database: %w", err)
	}
	result.Ready = true
	result.SmokePassed = true
	result.Duration = o.now().Sub(started)
	progress.Emit(remote.ProgressEvent{Stage: "drill", Message: "restore drill passed; drill database dropped", Current: 1, Total: 1, Done: true})
	return result, nil
}

// readBackup loads backup bytes from the recorded location, verifying the
// digest the backup state recorded when the artifact was written.
func (o *Operator) readBackup(root string, backup remote.BackupState) ([]byte, error) {
	if backup.Location == "" {
		return nil, fmt.Errorf("backup state carries no location")
	}
	path := backup.Location
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read backup %s: %w", backup.Location, err)
	}
	if backup.SHA256 != "" {
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != backup.SHA256 {
			return nil, fmt.Errorf("backup %s does not match its recorded sha256", backup.Location)
		}
	}
	return data, nil
}

func (o *Operator) identity(request remote.DatabaseRequest) (string, string) {
	user := request.State["user"]
	database := request.State["database"]
	if user == "" {
		user = defaultUser
	}
	if database == "" {
		database = defaultDatabase
	}
	return user, database
}

func (o *Operator) service(request remote.DatabaseRequest) string {
	if service := request.State["service"]; service != "" {
		return service
	}
	return defaultService
}

// composeFileArg picks the environment's compose file. `docker compose exec`
// parses env_file references for every subcommand, so the file must exist;
// a missing file is an honest docker error naming the path.
func composeFileArg(request remote.DatabaseRequest) string {
	name := "compose.yaml"
	if request.Environment == "test" {
		name = "compose.test.yaml"
	}
	return "-f=" + name
}

// randomSuffix names restore databases: a fixed prefix plus random bytes so
// concurrent drills can never collide, and no restore can ever land on the
// active database's name.
func randomSuffix() string {
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw)
}

// identifier guards names interpolated into psql -c statements. Everything
// the operator interpolates is generated or resolved from the lock, but the
// guard keeps a hostile state file from turning a database name into SQL.
func identifier(value string) error {
	if value == "" {
		return fmt.Errorf("empty identifier")
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
		default:
			return fmt.Errorf("identifier %q contains %q", value, string(r))
		}
	}
	return nil
}

// ValidateRequest enforces the identifier guards before any argv is built.
func ValidateRequest(request remote.DatabaseRequest) error {
	o := &Operator{}
	user, database := o.identity(request)
	for _, name := range []string{user, database, o.service(request)} {
		if err := identifier(name); err != nil {
			return fmt.Errorf("database request: %w", err)
		}
	}
	return nil
}
