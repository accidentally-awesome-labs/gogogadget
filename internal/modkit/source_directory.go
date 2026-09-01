package modkit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// directorySourceExclusions are paths whose presence must never reach the
// snapshot. Two kinds: tool caches, and files a developer has locally that a
// clone does not.
//
// The second kind is the one that bites. `.env` is gitignored, so every
// developer has one and CI has none — and while it was hashed, a working tree
// and a fresh checkout resolved DIFFERENT registry commits from identical
// source. That made the committed generated headers depend on who ran the
// generator, so CI's "generated code is committed and fresh" check could not
// pass from any real working tree. The registry can only ever distribute
// tracked source; anything else has no business in its identity.
var directorySourceExclusions = map[string]struct{}{
	".git":                  {},
	".superpowers":          {},
	"bin":                   {},
	"e2e/node_modules":      {},
	"e2e/playwright-report": {},
	"e2e/test-results":      {},
	"tmp":                   {},
}

// excludedSourceBases are names excluded wherever they appear, rather than at
// one fixed path. A build drops `tmp/` beside whatever package it is serving,
// and coverage output lands wherever it was invoked, so anchoring these to the
// root would leave the same class of divergence one directory down.
var excludedSourceBases = map[string]struct{}{
	".superpowers": {},
	"tmp":          {},
	"node_modules": {},
}

// excludedSourceFiles are local files, never distributed. `.env.example` IS
// distributed and generated, so it is handled by the generated-output rule
// rather than named here.
var excludedSourceFiles = map[string]struct{}{
	".env":            {},
	".env.local":      {},
	".DS_Store":       {},
	"coverage.out":    {},
	"cover.out":       {},
	".golangci.cache": {},
}

// isExcludedSourcePath reports whether a walked path is outside the registry's
// distributable identity, by exact path, by directory name at any depth, or by
// file name at any depth.
func isExcludedSourcePath(name string, isDir bool) bool {
	if _, ok := directorySourceExclusions[name]; ok {
		return true
	}
	base := path.Base(name)
	if isDir {
		_, ok := excludedSourceBases[base]
		return ok
	}
	_, ok := excludedSourceFiles[base]
	return ok
}

// DirectorySource resolves a local registry tree without using repository
// metadata. Its commit depends only on the included paths and file bytes.
type DirectorySource struct {
	Root string
}

func (s DirectorySource) Resolve(ctx context.Context, registry ProjectRegistry) (Snapshot, error) {
	if registry.Source == "" {
		registry.Source = "directory"
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, fmt.Errorf("resolve directory source: %w", err)
	}
	if s.Root == "" {
		return Snapshot{}, fmt.Errorf("resolve directory source: root is empty")
	}
	root := s.Root
	if registry.Path != "" && registry.Path != "." && registry.Path != s.Root {
		candidate := filepath.Join(s.Root, filepath.FromSlash(registry.Path))
		relative, relErr := filepath.Rel(s.Root, candidate)
		if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return Snapshot{}, fmt.Errorf("resolve directory source path %q: path escapes project root", registry.Path)
		}
		if info, statErr := os.Stat(filepath.Join(candidate, "registry.json")); statErr == nil && !info.IsDir() {
			root = candidate
		}
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return Snapshot{}, fmt.Errorf("stat directory source root %q: %w", root, err)
	}
	if rootInfo.Mode()&fs.ModeSymlink != 0 {
		return Snapshot{}, fmt.Errorf("validate directory source root %q: symlinks are not allowed", root)
	}
	if !rootInfo.IsDir() {
		return Snapshot{}, fmt.Errorf("validate directory source root %q: not a directory", root)
	}

	rootFS := os.DirFS(root)
	treeHash := sha256.New()
	fileDigests := make(map[string][sha256.Size]byte)
	fileSizes := make(map[string]int64)
	directories := map[string][]directorySnapshotEntry{".": nil}
	err = fs.WalkDir(rootFS, ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("read directory source entry %q: %w", name, walkErr)
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("walk directory source at %q: %w", name, err)
		}
		if name == "." {
			return nil
		}
		if isExcludedSourcePath(name, entry.IsDir()) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		// A self-hosting registry resolves from the tree it installs into, so the
		// snapshot must contain only what the registry can distribute. Project
		// state and tool-owned output are excluded: including them would let
		// writing the lock invalidate the lock that was just written, and
		// `sync --check` could never be clean.
		if !entry.IsDir() && (name == ProjectFileName || name == LockFileName || IsGeneratedOutputPath(name)) {
			return nil
		}
		if !fs.ValidPath(name) {
			return fmt.Errorf("validate directory source path %q: must be a valid relative slash path", name)
		}
		if err := validateSafePath(name); err != nil {
			return fmt.Errorf("validate directory source path %q: %w", name, err)
		}

		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat directory source entry %q: %w", name, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("validate directory source entry %q: symlinks are not allowed", name)
		}
		if info.IsDir() {
			directories[name] = nil
			parent := path.Dir(name)
			directories[parent] = append(directories[parent], directorySnapshotEntry{
				name: path.Base(name),
				mode: fs.ModeDir | 0o555,
			})
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("validate directory source entry %q: non-regular files are not allowed", name)
		}

		file, err := rootFS.Open(name)
		if err != nil {
			return fmt.Errorf("open directory source file %q: %w", name, err)
		}
		openedInfo, err := file.Stat()
		if err != nil {
			return closeDirectorySourceFile(file, name, fmt.Errorf("stat opened directory source file %q: %w", name, err))
		}
		if !openedInfo.Mode().IsRegular() {
			return closeDirectorySourceFile(file, name, fmt.Errorf("validate opened directory source file %q: non-regular files are not allowed", name))
		}
		if openedInfo.Size() < 0 {
			return closeDirectorySourceFile(file, name, fmt.Errorf("validate opened directory source file %q: negative size", name))
		}

		writeHashUint64(treeHash, uint64(len(name)))
		_, _ = treeHash.Write([]byte(name))
		writeHashUint64(treeHash, uint64(openedInfo.Size()))

		fileHash := sha256.New()
		readCount, copyErr := io.Copy(io.MultiWriter(treeHash, fileHash), &sourceContextReader{ctx: ctx, reader: file})
		closeErr := file.Close()
		if copyErr != nil {
			return errors.Join(
				fmt.Errorf("read directory source file %q: %w", name, copyErr),
				wrapDirectorySourceCloseError(name, closeErr),
			)
		}
		if closeErr != nil {
			return wrapDirectorySourceCloseError(name, closeErr)
		}
		if readCount != openedInfo.Size() {
			return fmt.Errorf("read directory source file %q: size changed during resolution (read %d, expected %d)", name, readCount, openedInfo.Size())
		}

		fileDigests[name] = [sha256.Size]byte(fileHash.Sum(nil))
		fileSizes[name] = openedInfo.Size()
		parent := path.Dir(name)
		directories[parent] = append(directories[parent], directorySnapshotEntry{
			name: path.Base(name),
			mode: 0o444,
			size: openedInfo.Size(),
		})
		return nil
	})
	if err != nil {
		return Snapshot{}, fmt.Errorf("resolve directory source %q: %w", s.Root, err)
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, fmt.Errorf("resolve directory source %q: %w", s.Root, err)
	}
	commit := hex.EncodeToString(treeHash.Sum(nil))
	rootMetadata, metadataErr := loadRegistryRoot(rootFS)
	if metadataErr != nil {
		return Snapshot{}, metadataErr
	}
	if registry.Namespace != "" && registry.Namespace != rootMetadata.Namespace {
		return Snapshot{}, fmt.Errorf("registry namespace %q does not match requested namespace %q", rootMetadata.Namespace, registry.Namespace)
	}
	verifyKey := registry.PublicKey
	if verifyKey != "" {
		if _, keyErr := RegistryKeyFingerprint(verifyKey); keyErr != nil {
			return Snapshot{}, keyErr
		}
	}
	snapshotDigest, verifyErr := verifySnapshotFiles(rootFS, verifyKey, true)
	if verifyErr != nil {
		return Snapshot{}, verifyErr
	}
	if snapshotDigest == "" {
		snapshotDigest = commit
	}
	return Snapshot{
		Commit: commit, SnapshotSHA256: snapshotDigest, CacheKey: snapshotDigest,
		Registry: rootMetadata,
		FS: &verifiedDirectoryFS{
			base: rootFS, digests: fileDigests, fileSizes: fileSizes, directories: directories,
		},
	}, nil
}

func writeHashUint64(destination hash.Hash, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = destination.Write(encoded[:])
}

type sourceContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *sourceContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

type verifiedDirectoryFS struct {
	base        fs.FS
	digests     map[string][sha256.Size]byte
	fileSizes   map[string]int64
	directories map[string][]directorySnapshotEntry
}

func (f *verifiedDirectoryFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if entries, ok := f.directories[name]; ok {
		return &directorySnapshotDir{
			path:    name,
			entries: entries,
		}, nil
	}
	if _, ok := f.digests[name]; !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	data, err := f.readVerifiedFile(name)
	if err != nil {
		return nil, err
	}
	return &directorySnapshotFile{
		path:   name,
		reader: bytes.NewReader(data),
		info: directorySnapshotEntry{
			name: path.Base(name),
			mode: 0o444,
			size: f.fileSizes[name],
		},
	}, nil
}

func (f *verifiedDirectoryFS) ReadFile(name string) ([]byte, error) {
	if _, ok := f.digests[name]; !ok {
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrNotExist}
	}
	return f.readVerifiedFile(name)
}

func (f *verifiedDirectoryFS) readVerifiedFile(name string) ([]byte, error) {
	data, err := fs.ReadFile(f.base, name)
	if err != nil {
		return nil, fmt.Errorf("read directory snapshot file %q: %w", name, err)
	}
	if sha256.Sum256(data) != f.digests[name] {
		return nil, fmt.Errorf("read directory snapshot file %q: content changed after resolution", name)
	}
	return data, nil
}

type directorySnapshotEntry struct {
	name string
	mode fs.FileMode
	size int64
}

func (e directorySnapshotEntry) Name() string       { return e.name }
func (e directorySnapshotEntry) Size() int64        { return e.size }
func (e directorySnapshotEntry) Mode() fs.FileMode  { return e.mode }
func (e directorySnapshotEntry) ModTime() time.Time { return time.Time{} }
func (e directorySnapshotEntry) IsDir() bool        { return e.mode.IsDir() }
func (e directorySnapshotEntry) Sys() any           { return nil }
func (e directorySnapshotEntry) Type() fs.FileMode  { return e.mode.Type() }
func (e directorySnapshotEntry) Info() (fs.FileInfo, error) {
	return e, nil
}

type directorySnapshotDir struct {
	path    string
	entries []directorySnapshotEntry
	offset  int
	closed  bool
}

func (d *directorySnapshotDir) Close() error {
	d.closed = true
	return nil
}

func (d *directorySnapshotDir) Stat() (fs.FileInfo, error) {
	if d.closed {
		return nil, &fs.PathError{Op: "stat", Path: d.path, Err: fs.ErrClosed}
	}
	return directorySnapshotEntry{name: path.Base(d.path), mode: fs.ModeDir | 0o555}, nil
}

func (d *directorySnapshotDir) Read([]byte) (int, error) {
	if d.closed {
		return 0, &fs.PathError{Op: "read", Path: d.path, Err: fs.ErrClosed}
	}
	return 0, &fs.PathError{Op: "read", Path: d.path, Err: fs.ErrInvalid}
}

func (d *directorySnapshotDir) ReadDir(count int) ([]fs.DirEntry, error) {
	if d.closed {
		return nil, &fs.PathError{Op: "readdir", Path: d.path, Err: fs.ErrClosed}
	}
	if count > 0 && d.offset >= len(d.entries) {
		return nil, io.EOF
	}
	end := len(d.entries)
	if count > 0 && count < end-d.offset {
		end = d.offset + count
	}
	entries := make([]fs.DirEntry, end-d.offset)
	for i := range entries {
		entries[i] = d.entries[d.offset+i]
	}
	d.offset = end
	return entries, nil
}

type directorySnapshotFile struct {
	path   string
	reader *bytes.Reader
	info   directorySnapshotEntry
	closed bool
}

func (f *directorySnapshotFile) Close() error {
	f.closed = true
	return nil
}

func (f *directorySnapshotFile) Stat() (fs.FileInfo, error) {
	if f.closed {
		return nil, &fs.PathError{Op: "stat", Path: f.path, Err: fs.ErrClosed}
	}
	return f.info, nil
}

func (f *directorySnapshotFile) Read(destination []byte) (int, error) {
	if f.closed {
		return 0, &fs.PathError{Op: "read", Path: f.path, Err: fs.ErrClosed}
	}
	return f.reader.Read(destination)
}

func closeDirectorySourceFile(file fs.File, name string, primary error) error {
	return errors.Join(primary, wrapDirectorySourceCloseError(name, file.Close()))
}

func wrapDirectorySourceCloseError(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("close directory source file %q: %w", name, err)
}
