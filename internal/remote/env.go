package remote

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Environment file names under .ggg/env. The CLI writes CLI-managed
// development and test values there at mode 0600; the file is gitignored and
// never loaded when APP_ENV is production.
const (
	EnvDirName     = ".ggg"
	EnvFileDirName = "env"
	StateFileName  = "state.json"
)

// EnvironmentEnvFile is the CLI-managed env file path for one environment,
// relative to the project root.
func EnvironmentEnvFile(environment string) string {
	return filepath.Join(EnvDirName, EnvFileDirName, environment+".env")
}

// LookupEnv resolves values in the declared order: the process environment
// wins, then the CLI-managed .ggg/env/<environment>.env file, then the
// legacy project .env — and the legacy file is read in development only.
// No file is opened at all for production.
//
// The returned func is safe for concurrent use and caches file contents at
// first use; callers that just wrote the file should construct a fresh
// lookup.
func LookupEnv(root, environment string) func(string) (string, bool) {
	var once sync.Once
	var layered []map[string]string
	load := func() {
		layered = readEnvLayers(root, environment)
	}
	return func(key string) (string, bool) {
		if value, ok := os.LookupEnv(key); ok && value != "" {
			return value, true
		}
		once.Do(load)
		for _, layer := range layered {
			if value, ok := layer[key]; ok && value != "" {
				return value, true
			}
		}
		return "", false
	}
}

// readEnvLayers returns the file layers in precedence order:
// .ggg/env/<environment>.env before the legacy .env, and the legacy layer
// only in development. A missing file is an empty layer.
//
// Production reads no file at all. WriteEnvironmentEnvFile refuses to create
// one, but an operator can still put a file there by hand, and the rule is
// that production configuration comes from the deployment environment — so
// this refuses to read it rather than relying on the file's absence.
func readEnvLayers(root, environment string) []map[string]string {
	if environment == "production" {
		return nil
	}
	layers := []map[string]string{parseEnvFile(filepath.Join(root, filepath.FromSlash(EnvironmentEnvFile(environment))))}
	if environment == "development" {
		layers = append(layers, parseEnvFile(filepath.Join(root, ".env")))
	}
	return layers
}

// SecretValuesFromEnv adapts LookupEnv to the SecretValues contract.
func SecretValuesFromEnv(root, environment string) SecretValues {
	return SecretValuesFunc(LookupEnv(root, environment))
}

// WriteEnvironmentEnvFile merges key/value pairs into the CLI-managed env
// file for one environment, creating it at mode 0600 when missing. An empty
// value removes the key. Production is refused unconditionally: the CLI
// never persists production secrets, and no command loads the file there.
func WriteEnvironmentEnvFile(root, environment string, values map[string]string) error {
	if environment != "development" && environment != "test" {
		return fmt.Errorf("refusing to write %s env values: only development and test are CLI-managed", environment)
	}
	path := filepath.Join(root, filepath.FromSlash(EnvironmentEnvFile(environment)))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	existing := parseEnvFile(path)
	for key, value := range values {
		if value == "" {
			delete(existing, key)
			continue
		}
		existing[key] = value
	}
	keys := make([]string, 0, len(existing))
	for key := range existing {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&b, "%s=%s\n", key, existing[key])
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

// EnvFileKeys reports the key names one env file declares, without values.
func EnvFileKeys(root, environment string) []string {
	path := filepath.Join(root, filepath.FromSlash(EnvironmentEnvFile(environment)))
	file := parseEnvFile(path)
	keys := make([]string, 0, len(file))
	for key := range file {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// parseEnvFile reads a KEY=VALUE file the same way config.loadDotEnv parses
// one, but into a map instead of the process environment. A missing or
// malformed file yields what parsed; secrets live here, so parse errors are
// never fatal noise.
func parseEnvFile(path string) map[string]string {
	values := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return values
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		if key != "" {
			values[key] = value
		}
	}
	return values
}
