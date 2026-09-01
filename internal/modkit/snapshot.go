package modkit

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	RegistrySnapshotPath  = "registry.snapshot.json"
	RegistrySignaturePath = "registry.snapshot.sig"
)

type SnapshotFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type RegistrySnapshot struct {
	Schema   int            `json:"schema"`
	Registry RegistryRoot   `json:"registry"`
	Files    []SnapshotFile `json:"files"`
}

func loadRegistryRoot(fsys fs.FS) (RegistryRoot, error) {
	var root RegistryRoot
	data, err := fs.ReadFile(fsys, "registry.json")
	if err != nil {
		return root, fmt.Errorf("read registry.json: %w", err)
	}
	if err := rejectDuplicateJSONFields(data); err != nil {
		return root, fmt.Errorf("decode registry.json: %w", err)
	}
	if err := decodeStrict(data, &root); err != nil {
		return root, fmt.Errorf("decode registry.json: %w", err)
	}
	if root.Schema != 2 || !validNamespace(root.Namespace) || !validPackagePath(root.CanonicalModule) {
		return root, fmt.Errorf("registry.json has invalid schema, namespace, or canonical_module")
	}
	return root, nil
}

func BuildRegistrySnapshot(fsys fs.FS) ([]byte, error) {
	root, err := loadRegistryRoot(fsys)
	if err != nil {
		return nil, err
	}
	files := []SnapshotFile{}
	err = fs.WalkDir(fsys, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == "." || name == RegistrySnapshotPath || name == RegistrySignaturePath {
			return nil
		}
		if name != "registry.json" && name != "registry" && !strings.HasPrefix(name, "registry/") {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if isExcludedSourcePath(name, entry.IsDir()) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		files = append(files, SnapshotFile{Path: name, SHA256: hex.EncodeToString(sum[:])})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("build registry snapshot: %w", err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return marshalIndented(RegistrySnapshot{Schema: 1, Registry: root, Files: files})
}

func WriteRegistrySnapshot(root string) ([]byte, error) {
	data, err := BuildRegistrySnapshot(os.DirFS(root))
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(root, ".registry-snapshot-*.json")
	if err != nil {
		return nil, err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(name, filepath.Join(root, RegistrySnapshotPath)); err != nil {
		return nil, err
	}
	if err := os.Chmod(filepath.Join(root, RegistrySnapshotPath), 0o644); err != nil {
		return nil, err
	}
	return data, nil
}

func WriteSignedRegistrySnapshot(root string, private ed25519.PrivateKey) ([]byte, error) {
	if len(private) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("registry signing key must be %d bytes", ed25519.PrivateKeySize)
	}
	data, err := BuildRegistrySnapshot(os.DirFS(root))
	if err != nil {
		return nil, err
	}
	sig := ed25519.Sign(private, data)
	tmp, err := os.MkdirTemp(root, ".registry-snapshot-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err := os.WriteFile(filepath.Join(tmp, RegistrySnapshotPath), data, 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(tmp, RegistrySignaturePath), []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0o644); err != nil {
		return nil, err
	}
	for _, name := range []string{RegistrySnapshotPath, RegistrySignaturePath} {
		if err := os.Rename(filepath.Join(tmp, name), filepath.Join(root, name)); err != nil {
			return nil, err
		}
	}
	return data, nil
}

func RegistryKeyFingerprint(publicKey string) (string, error) {
	key, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return "", fmt.Errorf("registry public key must be base64 raw %d bytes", ed25519.PublicKeySize)
	}
	sum := sha256.Sum256(key)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// RegistryPrivateKeyFromSeed derives a reproducible Ed25519 signing key from
// exactly one 32-byte seed. Registry publishers can use this in deterministic
// fixture/build tooling without ever serializing the private key in metadata.
func RegistryPrivateKeyFromSeed(seed []byte) (ed25519.PrivateKey, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("registry signing seed must be %d bytes", ed25519.SeedSize)
	}
	return ed25519.NewKeyFromSeed(append([]byte(nil), seed...)), nil
}

func verifySnapshotFiles(fsys fs.FS, publicKey string, allowUnsigned bool) (string, error) {
	data, err := fs.ReadFile(fsys, RegistrySnapshotPath)
	if errors.Is(err, fs.ErrNotExist) {
		if allowUnsigned {
			return "", nil
		}
		return "", fmt.Errorf("signed registry snapshot is required")
	}
	if err != nil {
		return "", err
	}
	if publicKey == "" {
		if !allowUnsigned {
			return "", fmt.Errorf("signed registry snapshot requires public_key")
		}
	} else {
		key, e := base64.StdEncoding.DecodeString(publicKey)
		if e != nil || len(key) != ed25519.PublicKeySize {
			return "", fmt.Errorf("registry public_key is not base64 raw %d bytes", ed25519.PublicKeySize)
		}
		sigData, e := fs.ReadFile(fsys, RegistrySignaturePath)
		if e != nil {
			return "", fmt.Errorf("read %s: %w", RegistrySignaturePath, e)
		}
		sig, e := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigData)))
		if e != nil || len(sig) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(key), data, sig) {
			return "", fmt.Errorf("registry snapshot signature verification failed")
		}
	}
	var snap RegistrySnapshot
	if err := decodeStrict(data, &snap); err != nil {
		return "", fmt.Errorf("decode %s: %w", RegistrySnapshotPath, err)
	}
	if snap.Schema != 1 || snap.Registry.Schema != 2 {
		return "", fmt.Errorf("registry snapshot schema is invalid")
	}
	seen := map[string]struct{}{}
	for _, f := range snap.Files {
		if !fs.ValidPath(f.Path) || f.Path == RegistrySnapshotPath || f.Path == RegistrySignaturePath || f.SHA256 == "" {
			return "", fmt.Errorf("registry snapshot lists unsafe file %q", f.Path)
		}
		if _, ok := seen[f.Path]; ok {
			return "", fmt.Errorf("registry snapshot lists %q more than once", f.Path)
		}
		seen[f.Path] = struct{}{}
		payload, e := fs.ReadFile(fsys, f.Path)
		if e != nil {
			return "", fmt.Errorf("registry snapshot payload %q is missing: %w", f.Path, e)
		}
		sum := sha256.Sum256(payload)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), f.SHA256) {
			return "", fmt.Errorf("registry snapshot payload %q digest mismatch", f.Path)
		}
	}
	if err := fs.WalkDir(fsys, ".", func(name string, entry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if entry.IsDir() || name == "." || name == RegistrySnapshotPath || name == RegistrySignaturePath || isExcludedSourcePath(name, false) {
			return nil
		}
		if strings.HasPrefix(name, "registry/") {
			if _, ok := seen[name]; !ok {
				return fmt.Errorf("registry snapshot has unlisted payload %q", name)
			}
		}
		return nil
	}); err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
