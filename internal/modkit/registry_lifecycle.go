package modkit

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Registry authoring file names. The private key never enters a snapshot or
// the lock: it exists only on the publisher's disk, and every consumer pins
// the matching base64 public key in gogogadget.json.
const (
	RegistryPrivateKeyEnv = "GGG_REGISTRY_SIGNING_KEY"
	RegistryPublicKeyEnv  = "GGG_REGISTRY_PUBLIC_KEY"

	// RegistryKeyRotationPath is the canonical key-rotation record. A rotation
	// is accepted only after both signatures verify — the old signature under
	// the consumer's pinned key and the new signature under the declared key —
	// and the wall clock has reached not_before.
	RegistryKeyRotationPath    = "registry-key-rotation.json"
	registryRotationOldSigPath = "registry.snapshot.old.sig"
	registryRotationNewSigPath = "registry.snapshot.new.sig"
)

// RegistryKeyRotation is the canonical record a publisher writes once when
// rotating a registry signing key. Dates are RFC3339 UTC.
type RegistryKeyRotation struct {
	Namespace      string `json:"namespace"`
	OldFingerprint string `json:"old_fingerprint"`
	NewPublicKey   string `json:"new_public_key"`
	NotBefore      string `json:"not_before"`
}

// snapshotTimeNow is the wall clock used for rotation activation. Tests
// override it; production always reads the real clock.
var snapshotTimeNow = time.Now

// GenerateRegistryKeyPair creates a fresh Ed25519 key pair and writes the
// base64 keys to the given paths. The private key is written 0600, the public
// key 0644, and any existing file at either path is refused rather than
// silently overwritten.
func GenerateRegistryKeyPair(privatePath, publicPath string) (string, error) {
	if privatePath == "" || publicPath == "" {
		return "", fmt.Errorf("both --private and --public paths are required")
	}
	for _, path := range []struct {
		path string
		what string
	}{{privatePath, "private key"}, {publicPath, "public key"}} {
		if _, err := os.Stat(path.path); err == nil {
			return "", fmt.Errorf("refusing to overwrite existing %s %s", path.what, path.path)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	if err := writeKeyFile(privatePath, base64.StdEncoding.EncodeToString(private), 0o600); err != nil {
		return "", err
	}
	if err := writeKeyFile(publicPath, base64.StdEncoding.EncodeToString(public), 0o644); err != nil {
		return "", err
	}
	return RegistryKeyFingerprint(base64.StdEncoding.EncodeToString(public))
}

func writeKeyFile(path, content string, mode os.FileMode) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(content+"\n"), mode)
}

// LoadRegistryPrivateKey reads a base64 Ed25519 private key from disk.
func LoadRegistryPrivateKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read signing key %s: %w", path, err)
	}
	return decodeRegistryPrivateKey(strings.TrimSpace(string(data)))
}

// RegistryPrivateKeyFromEnv decodes the base64 signing key carried in
// GGG_REGISTRY_SIGNING_KEY. CI signs from the environment without ever
// writing the key to disk.
func RegistryPrivateKeyFromEnv(value string) (ed25519.PrivateKey, error) {
	return decodeRegistryPrivateKey(strings.TrimSpace(value))
}

func decodeRegistryPrivateKey(value string) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("registry signing key must be base64 raw %d bytes", ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(raw), nil
}

// SignRegistrySnapshot writes the snapshot and its Ed25519 signature into the
// registry tree rooted at root.
func SignRegistrySnapshot(root string, private ed25519.PrivateKey) ([]byte, error) {
	return WriteSignedRegistrySnapshot(root, private)
}

// VerifyRegistrySnapshot verifies registry.snapshot.json against its
// signature under the pinned public key, checks every listed payload digest,
// and returns the snapshot digest. A key-rotation record, when present, must
// dual-verify before the declared new key is honored.
func VerifyRegistrySnapshot(root, publicKey string) (string, error) {
	return verifySnapshotFiles(os.DirFS(root), publicKey, false)
}

// registryRotationEffectiveKey reports the effective public key after
// applying a key-rotation record. An absent record returns the pinned key
// unchanged with no fallback signature. A present record must dual-verify —
// the old signature under the pinned key and the new signature under the
// declared key — before either key is honored, and a record that declares the
// pinned key as its own replacement is treated as a completed rotation whose
// primary signature already verifies under the pinned key. Until not_before
// the pinned key stays effective and the primary signature (still signed by
// the outgoing key) verifies; from not_before the declared key is effective
// and the detached new signature carries the transition. The second return is
// the fallback signature path for that transition, empty when the primary
// signature must verify directly.
func registryRotationEffectiveKey(fsys fs.FS, publicKey string) (string, string, error) {
	data, err := fs.ReadFile(fsys, RegistryKeyRotationPath)
	if errors.Is(err, fs.ErrNotExist) {
		return publicKey, "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("read %s: %w", RegistryKeyRotationPath, err)
	}
	if publicKey == "" {
		return "", "", fmt.Errorf("key rotation %s requires a pinned public key", RegistryKeyRotationPath)
	}
	var rotation RegistryKeyRotation
	if err := decodeStrict(data, &rotation); err != nil {
		return "", "", fmt.Errorf("decode %s: %w", RegistryKeyRotationPath, err)
	}
	if rotation.Namespace == "" || rotation.NewPublicKey == "" || rotation.NotBefore == "" {
		return "", "", fmt.Errorf("%s requires namespace, new_public_key, and not_before", RegistryKeyRotationPath)
	}
	newFingerprint, err := RegistryKeyFingerprint(rotation.NewPublicKey)
	if err != nil {
		return "", "", fmt.Errorf("key rotation new_public_key: %w", err)
	}
	pinnedFingerprint, err := RegistryKeyFingerprint(publicKey)
	if err != nil {
		return "", "", err
	}
	if newFingerprint == pinnedFingerprint {
		// The pinned key is already the declared replacement: the rotation
		// completed for this consumer and the primary signature verifies
		// under the pinned key like any ordinary snapshot.
		return publicKey, "", nil
	}
	if rotation.OldFingerprint != "" && rotation.OldFingerprint != pinnedFingerprint {
		return "", "", fmt.Errorf(
			"key rotation old_fingerprint %s does not match pinned key %s", rotation.OldFingerprint, pinnedFingerprint)
	}
	if err := verifyDetachedSnapshotSignature(fsys, publicKey, registryRotationOldSigPath); err != nil {
		return "", "", fmt.Errorf("key rotation old signature: %w", err)
	}
	if err := verifyDetachedSnapshotSignature(fsys, rotation.NewPublicKey, registryRotationNewSigPath); err != nil {
		return "", "", fmt.Errorf("key rotation new signature: %w", err)
	}
	notBefore, err := time.Parse(time.RFC3339, rotation.NotBefore)
	if err != nil {
		return "", "", fmt.Errorf("key rotation not_before must be RFC3339: %w", err)
	}
	if snapshotTimeNow().UTC().Before(notBefore) {
		return publicKey, registryRotationOldSigPath, nil
	}
	return rotation.NewPublicKey, registryRotationNewSigPath, nil
}

func verifyDetachedSnapshotSignature(fsys fs.FS, publicKey, signaturePath string) error {
	key, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return fmt.Errorf("public key is not base64 raw %d bytes", ed25519.PublicKeySize)
	}
	data, err := fs.ReadFile(fsys, RegistrySnapshotPath)
	if err != nil {
		return err
	}
	sigData, err := fs.ReadFile(fsys, signaturePath)
	if err != nil {
		return err
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigData)))
	if err != nil || len(sig) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(key), data, sig) {
		return fmt.Errorf("signature verification failed")
	}
	return nil
}

// WriteRegistryKeyRotation publishes a key rotation: it writes the canonical
// rotation record plus detached signatures over the current snapshot under
// both the outgoing and the incoming key. The primary snapshot signature
// stays under the outgoing key — it is the key every consumer trusts until
// not_before — and consumers past not_before verify the detached new
// signature instead. notBefore must be RFC3339; publishers SHOULD date it in
// the future so every consumer pins the old key before the new one activates,
// and SHOULD republish a snapshot without the rotation record once the new
// key is broadly pinned.
func WriteRegistryKeyRotation(root string, oldKey, newKey ed25519.PrivateKey, notBefore string) error {
	if len(oldKey) != ed25519.PrivateKeySize || len(newKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("rotation requires two base64 raw %d-byte private keys", ed25519.PrivateKeySize)
	}
	stamp, err := time.Parse(time.RFC3339, notBefore)
	if err != nil {
		return fmt.Errorf("not_before must be RFC3339: %w", err)
	}
	oldPublicBytes, ok := oldKey.Public().(ed25519.PublicKey)
	if !ok {
		return fmt.Errorf("old signing key has an invalid public part")
	}
	newPublicBytes, ok := newKey.Public().(ed25519.PublicKey)
	if !ok {
		return fmt.Errorf("new signing key has an invalid public part")
	}
	oldPublicKey := base64.StdEncoding.EncodeToString(oldPublicBytes)
	newPublicKey := base64.StdEncoding.EncodeToString(newPublicBytes)
	oldFingerprint, err := RegistryKeyFingerprint(oldPublicKey)
	if err != nil {
		return err
	}
	record := RegistryKeyRotation{
		Namespace:      "",
		OldFingerprint: oldFingerprint,
		NewPublicKey:   newPublicKey,
		NotBefore:      stamp.UTC().Format(time.RFC3339),
	}
	if rootMeta, rootErr := loadRegistryRoot(os.DirFS(root)); rootErr == nil {
		record.Namespace = rootMeta.Namespace
	}
	// The primary signature stays under the outgoing key: until not_before
	// every consumer's pinned key verifies it directly.
	data, err := WriteSignedRegistrySnapshot(root, oldKey)
	if err != nil {
		return err
	}
	oldSig := ed25519.Sign(oldKey, data)
	newSig := ed25519.Sign(newKey, data)
	recordData, err := marshalIndented(record)
	if err != nil {
		return err
	}
	payloads := []struct {
		path string
		body []byte
	}{
		{filepath.Join(root, RegistryKeyRotationPath), append(recordData, '\n')},
		{filepath.Join(root, registryRotationOldSigPath), []byte(base64.StdEncoding.EncodeToString(oldSig) + "\n")},
		{filepath.Join(root, registryRotationNewSigPath), []byte(base64.StdEncoding.EncodeToString(newSig) + "\n")},
	}
	for _, payload := range payloads {
		if err := os.WriteFile(payload.path, payload.body, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// InitRegistryTree scaffolds a publishable registry: the registry.json root,
// one empty index per kind, and a .gitignore that keeps signing keys out of
// version control. Existing files are never overwritten, so re-running init
// on a lived-in registry is a safe no-op for everything already present.
func InitRegistryTree(dir, namespace, canonicalModule string) ([]string, error) {
	if !validNamespace(namespace) {
		return nil, fmt.Errorf("namespace %q is not a valid registry namespace", namespace)
	}
	if !validPackagePath(canonicalModule) {
		return nil, fmt.Errorf("canonical module %q is not a valid Go module path", canonicalModule)
	}
	if dir == "" {
		return nil, fmt.Errorf("registry directory is required")
	}
	written := make([]string, 0, len(catalogIncludes)+2)
	root := RegistryRoot{
		Schema:          2,
		Namespace:       namespace,
		CanonicalModule: canonicalModule,
		Includes:        make([]string, 0, len(catalogIncludes)),
	}
	for _, include := range catalogIncludes {
		root.Includes = append(root.Includes, include.path)
	}
	data, err := marshalIndented(root)
	if err != nil {
		return nil, err
	}
	if err := writeRegistryScaffoldFile(dir, "registry.json", data, &written); err != nil {
		return nil, err
	}
	for _, include := range catalogIncludes {
		index := CatalogIndex{Schema: 2, Kind: include.kind, Items: []string{}}
		data, err := marshalIndented(index)
		if err != nil {
			return nil, err
		}
		if err := writeRegistryScaffoldFile(dir, include.path, data, &written); err != nil {
			return nil, err
		}
	}
	gitignore := "# Registry signing keys never enter version control.\n*.key\n*.pem\nregistry-private-key*\n"
	if err := writeRegistryScaffoldFile(dir, ".gitignore", []byte(gitignore), &written); err != nil {
		return nil, err
	}
	sort.Strings(written)
	return written, nil
}

func writeRegistryScaffoldFile(dir, rel string, content []byte, written *[]string) error {
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if _, err := os.Stat(full); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if parent := filepath.Dir(full); parent != "." {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(full, append(content, '\n'), 0o644); err != nil {
		return err
	}
	*written = append(*written, rel)
	return nil
}
