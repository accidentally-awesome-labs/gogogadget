package modkit

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultGitHubAPIBaseURL      = "https://api.github.com"
	defaultGitHubCodeloadBaseURL = "https://codeload.github.com"
	defaultGitHubCacheDir        = ""
	githubUserAgent              = "gogogadget-ggg/1"
	githubJSONAccept             = "application/vnd.github+json"
	maxGitHubAPIResponse         = int64(1 << 20)
	maxGitHubErrorBody           = int64(8 << 10)
	maxGitHubArchive             = int64(128 << 20)
	maxGitHubExpanded            = int64(1 << 30)
	maxGitHubEntries             = 200_000
)

// GitHubSource resolves refs through the GitHub commits API and materializes
// immutable codeload archives in a content-addressed local cache.
type GitHubSource struct {
	Client          *http.Client
	CacheDir        string
	APIBaseURL      string
	CodeloadBaseURL string
	Token           string
	Offline         bool
}

func (s GitHubSource) Resolve(ctx context.Context, registry ProjectRegistry) (Snapshot, error) {
	repository, ref := registry.Repository, registry.Ref
	if registry.Source != "github" {
		return Snapshot{}, fmt.Errorf("resolve GitHub source: source must be github")
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, fmt.Errorf("resolve GitHub source: %w", err)
	}
	if err := validateRepository(repository); err != nil {
		return Snapshot{}, fmt.Errorf("resolve GitHub source repository %q: %w", repository, err)
	}
	if ref == "" {
		return Snapshot{}, fmt.Errorf("resolve GitHub source %q: ref is empty", repository)
	}

	cacheDir := s.CacheDir
	if cacheDir == "" {
		cacheRoot, cacheErr := os.UserCacheDir()
		if cacheErr != nil {
			return Snapshot{}, fmt.Errorf("locate registry cache: %w", cacheErr)
		}
		cacheDir = filepath.Join(cacheRoot, "ggg", "registry")
	}

	if s.Offline {
		if !validGitHubCommit(ref) {
			return Snapshot{}, fmt.Errorf("resolve GitHub source %q offline: ref must be a full 40-character lowercase commit", repository)
		}
		exists, err := validateExistingGitHubCacheDir(cacheDir)
		if err != nil {
			return Snapshot{}, fmt.Errorf("resolve GitHub source %q at %s offline: %w", repository, ref, err)
		}
		if !exists {
			return Snapshot{}, fmt.Errorf("resolve GitHub source %q at %s offline: verified cache entry not found", repository, ref)
		}
		snapshot, found, err := githubCachedSnapshotForCommit(ctx, cacheDir, ref)
		if err != nil {
			return Snapshot{}, fmt.Errorf("resolve GitHub source %q at %s offline: %w", repository, ref, err)
		}
		if !found {
			return Snapshot{}, fmt.Errorf("resolve GitHub source %q at %s offline: verified cache entry not found", repository, ref)
		}
		return validateGitHubSnapshot(snapshot, registry)
	}

	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	apiBaseURL := s.APIBaseURL
	if apiBaseURL == "" {
		apiBaseURL = defaultGitHubAPIBaseURL
	}
	codeloadBaseURL := s.CodeloadBaseURL
	if codeloadBaseURL == "" {
		codeloadBaseURL = defaultGitHubCodeloadBaseURL
	}

	commit, err := s.resolveGitHubCommit(ctx, client, apiBaseURL, repository, ref)
	if err != nil {
		return Snapshot{}, err
	}
	if err := ensureGitHubCacheDir(cacheDir); err != nil {
		return Snapshot{}, err
	}
	if snapshot, found, err := githubCachedSnapshotForCommit(ctx, cacheDir, commit); err != nil {
		return Snapshot{}, fmt.Errorf("open GitHub cache for %q at %s: %w", repository, commit, err)
	} else if found {
		return validateGitHubSnapshot(snapshot, registry)
	}
	snapshot, err := s.populateGitHubCache(ctx, client, codeloadBaseURL, cacheDir, repository, commit, registry)
	if err != nil {
		return Snapshot{}, err
	}
	return validateGitHubSnapshot(snapshot, registry)
}
func validateGitHubSnapshot(snapshot Snapshot, registry ProjectRegistry) (Snapshot, error) {
	if snapshot.FS == nil {
		return Snapshot{}, fmt.Errorf("GitHub snapshot has no filesystem")
	}
	digest, err := verifySnapshotFiles(snapshot.FS, registry.PublicKey, false)
	if err != nil {
		return Snapshot{}, fmt.Errorf("verify signed GitHub registry: %w", err)
	}
	root, err := loadRegistryRoot(snapshot.FS)
	if err != nil {
		return Snapshot{}, err
	}
	if registry.Namespace != "" && root.Namespace != registry.Namespace {
		return Snapshot{}, fmt.Errorf("registry namespace %q does not match requested namespace %q", root.Namespace, registry.Namespace)
	}
	snapshot.Registry, snapshot.SnapshotSHA256, snapshot.CacheKey = root, digest, digest
	return snapshot, nil
}

func (s GitHubSource) resolveGitHubCommit(ctx context.Context, client *http.Client, apiBaseURL, repository, ref string) (string, error) {
	owner, repo, _ := strings.Cut(repository, "/")
	requestURL := strings.TrimRight(apiBaseURL, "/") + "/repos/" + owner + "/" + repo + "/commits/" + url.PathEscape(ref)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return "", fmt.Errorf("create GitHub ref request for %q at %q: %w", repository, ref, err)
	}
	s.setGitHubHeaders(request, githubJSONAccept)

	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("request GitHub ref for %q at %q: %w", repository, ref, err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		text, readErr := readGitHubErrorResponse(response)
		if readErr != nil {
			return "", fmt.Errorf("read GitHub ref error response for %q at %q: %w", repository, ref, readErr)
		}
		return "", fmt.Errorf("resolve GitHub ref for %q at %q: HTTP %s: %s", repository, ref, response.Status, text)
	}

	body, err := readAndCloseBounded(response.Body, maxGitHubAPIResponse, "GitHub ref response")
	if err != nil {
		return "", fmt.Errorf("read GitHub ref response for %q at %q: %w", repository, ref, err)
	}
	var payload struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decode GitHub ref response for %q at %q: %w", repository, ref, err)
	}
	if !validGitHubCommit(payload.SHA) {
		return "", fmt.Errorf("decode GitHub ref response for %q at %q: sha %q is not a 40-character lowercase commit", repository, ref, payload.SHA)
	}
	return payload.SHA, nil
}

func (s GitHubSource) populateGitHubCache(ctx context.Context, client *http.Client, codeloadBaseURL, cacheDir, repository, commit string, registry ProjectRegistry) (snapshot Snapshot, err error) {
	stage, err := os.MkdirTemp(cacheDir, "."+commit+"-")
	if err != nil {
		return Snapshot{}, fmt.Errorf("create staged GitHub cache in %q: %w", cacheDir, err)
	}
	defer func() {
		if stage == "" {
			return
		}
		if cleanupErr := os.RemoveAll(stage); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("clean staged GitHub cache %q: %w", stage, cleanupErr))
		}
	}()

	archivePath := filepath.Join(stage, "archive.tar.gz")
	if err := s.downloadGitHubArchive(ctx, client, codeloadBaseURL, repository, commit, archivePath); err != nil {
		return Snapshot{}, err
	}
	treePath := filepath.Join(stage, "tree")
	if err := extractGitHubArchive(ctx, archivePath, treePath); err != nil {
		return Snapshot{}, fmt.Errorf("extract GitHub archive for %q at %s: %w", repository, commit, err)
	}
	if err := verifyGitHubTree(ctx, treePath); err != nil {
		return Snapshot{}, fmt.Errorf("verify staged GitHub cache for %q at %s: %w", repository, commit, err)
	}
	snapshotDigest, err := verifySnapshotFiles(os.DirFS(treePath), registry.PublicKey, false)
	if err != nil {
		return Snapshot{}, fmt.Errorf("verify staged signed GitHub registry for %q at %s: %w", repository, commit, err)
	}
	if snapshotDigest == "" {
		return Snapshot{}, fmt.Errorf("verify staged signed GitHub registry for %q at %s: empty snapshot digest", repository, commit)
	}
	if err := os.WriteFile(filepath.Join(stage, "commit"), []byte(commit+"\n"), 0o600); err != nil {
		return Snapshot{}, fmt.Errorf("record GitHub snapshot commit %q: %w", commit, err)
	}

	finalPath := filepath.Join(cacheDir, snapshotDigest)
	if err := os.Rename(stage, finalPath); err != nil {
		winner, found, winnerErr := githubCachedSnapshot(ctx, cacheDir, snapshotDigest)
		if winnerErr != nil {
			return Snapshot{}, errors.Join(
				fmt.Errorf("install staged GitHub cache %q as %q: %w", stage, finalPath, err),
				fmt.Errorf("verify concurrent GitHub cache winner at %q: %w", finalPath, winnerErr),
			)
		}
		if found {
			return winner, nil
		}
		return Snapshot{}, fmt.Errorf("install staged GitHub cache %q as %q: %w", stage, finalPath, err)
	}
	stage = ""

	installed, found, err := githubCachedSnapshot(ctx, cacheDir, snapshotDigest)
	if err != nil {
		return Snapshot{}, fmt.Errorf("verify installed GitHub cache at %q: %w", finalPath, err)
	}
	if !found {
		return Snapshot{}, fmt.Errorf("verify installed GitHub cache at %q: cache disappeared after atomic install", finalPath)
	}
	return installed, nil
}

func (s GitHubSource) downloadGitHubArchive(ctx context.Context, client *http.Client, codeloadBaseURL, repository, commit, destination string) (err error) {
	owner, repo, _ := strings.Cut(repository, "/")
	requestURL := strings.TrimRight(codeloadBaseURL, "/") + "/" + owner + "/" + repo + "/tar.gz/" + commit
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return fmt.Errorf("create GitHub archive request for %q at %s: %w", repository, commit, err)
	}
	s.setGitHubHeaders(request, "application/octet-stream")

	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("request GitHub archive for %q at %s: %w", repository, commit, err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		text, readErr := readGitHubErrorResponse(response)
		if readErr != nil {
			return fmt.Errorf("read GitHub archive error response for %q at %s: %w", repository, commit, readErr)
		}
		return fmt.Errorf("download GitHub archive for %q at %s: HTTP %s: %s", repository, commit, response.Status, text)
	}
	defer closeSource(&err, response.Body, "GitHub archive response body")
	if response.ContentLength > maxGitHubArchive {
		return fmt.Errorf("download GitHub archive for %q at %s: compressed size %d exceeds %d-byte limit", repository, commit, response.ContentLength, maxGitHubArchive)
	}

	archiveFile, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create staged GitHub archive %q: %w", destination, err)
	}
	defer closeSource(&err, archiveFile, "staged GitHub archive "+destination)

	limited := io.LimitReader(&sourceContextReader{ctx: ctx, reader: response.Body}, maxGitHubArchive+1)
	written, err := io.Copy(archiveFile, limited)
	if err != nil {
		return fmt.Errorf("write staged GitHub archive %q: %w", destination, err)
	}
	if written > maxGitHubArchive {
		return fmt.Errorf("download GitHub archive for %q at %s: compressed data exceeds %d-byte limit", repository, commit, maxGitHubArchive)
	}
	if err := archiveFile.Sync(); err != nil {
		return fmt.Errorf("sync staged GitHub archive %q: %w", destination, err)
	}
	return nil
}

func (s GitHubSource) setGitHubHeaders(request *http.Request, accept string) {
	request.Header.Set("User-Agent", githubUserAgent)
	request.Header.Set("Accept", accept)
	if s.Token != "" {
		request.Header.Set("Authorization", "Bearer "+s.Token)
	}
}

func ensureGitHubCacheDir(cacheDir string) error {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return fmt.Errorf("create GitHub cache directory %q: %w", cacheDir, err)
	}
	exists, err := validateExistingGitHubCacheDir(cacheDir)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("validate GitHub cache directory %q: directory disappeared after creation", cacheDir)
	}
	return nil
}

func validateExistingGitHubCacheDir(cacheDir string) (bool, error) {
	info, err := os.Lstat(cacheDir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat GitHub cache directory %q: %w", cacheDir, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return false, fmt.Errorf("validate GitHub cache directory %q: symlinks are not allowed", cacheDir)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("validate GitHub cache directory %q: not a directory", cacheDir)
	}
	return true, nil
}

func githubCachedSnapshot(ctx context.Context, cacheDir, cacheKey string) (Snapshot, bool, error) {
	entryPath := filepath.Join(cacheDir, cacheKey)
	entryInfo, err := os.Lstat(entryPath)
	if errors.Is(err, fs.ErrNotExist) {
		return Snapshot{}, false, nil
	}
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("stat GitHub cache entry %q: %w", entryPath, err)
	}
	if entryInfo.Mode()&fs.ModeSymlink != 0 || !entryInfo.IsDir() {
		return Snapshot{}, false, fmt.Errorf("validate GitHub cache entry %q: expected a non-symlink directory", entryPath)
	}
	if !validSHA256(cacheKey) {
		return Snapshot{}, false, fmt.Errorf("validate GitHub cache key %q: expected snapshot digest", cacheKey)
	}
	commitData, err := os.ReadFile(filepath.Join(entryPath, "commit"))
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("read cached GitHub commit %q: %w", entryPath, err)
	}
	commit := strings.TrimSpace(string(commitData))
	if !validGitHubCommit(commit) {
		return Snapshot{}, false, fmt.Errorf("cached GitHub commit %q is invalid", commit)
	}

	archivePath := filepath.Join(entryPath, "archive.tar.gz")
	archiveInfo, err := os.Lstat(archivePath)
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("stat cached GitHub archive %q: %w", archivePath, err)
	}
	if archiveInfo.Mode()&fs.ModeSymlink != 0 || !archiveInfo.Mode().IsRegular() {
		return Snapshot{}, false, fmt.Errorf("validate cached GitHub archive %q: expected a non-symlink regular file", archivePath)
	}
	treePath := filepath.Join(entryPath, "tree")
	treeInfo, err := os.Lstat(treePath)
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("stat cached GitHub tree %q: %w", treePath, err)
	}
	if treeInfo.Mode()&fs.ModeSymlink != 0 || !treeInfo.IsDir() {
		return Snapshot{}, false, fmt.Errorf("validate cached GitHub tree %q: expected a non-symlink directory", treePath)
	}
	if err := verifyGitHubTree(ctx, treePath); err != nil {
		return Snapshot{}, false, err
	}
	return Snapshot{Commit: commit, SnapshotSHA256: cacheKey, CacheKey: cacheKey, FS: os.DirFS(treePath)}, true, nil
}

func githubCachedSnapshotForCommit(ctx context.Context, cacheDir, commit string) (Snapshot, bool, error) {
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Snapshot{}, false, nil
		}
		return Snapshot{}, false, err
	}
	var result Snapshot
	found := false
	for _, entry := range entries {
		if !entry.IsDir() || !validSHA256(entry.Name()) {
			continue
		}
		snapshot, ok, err := githubCachedSnapshot(ctx, cacheDir, entry.Name())
		if err != nil {
			return Snapshot{}, false, err
		}
		if ok && snapshot.Commit == commit {
			if found {
				return Snapshot{}, false, fmt.Errorf("multiple cached snapshots found for GitHub commit %q", commit)
			}
			result, found = snapshot, true
		}
	}
	return result, found, nil
}

func verifyGitHubTree(ctx context.Context, treePath string) error {
	registryFound := false
	err := fs.WalkDir(os.DirFS(treePath), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("read cached GitHub tree entry %q: %w", name, walkErr)
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("verify cached GitHub tree at %q: %w", name, err)
		}
		if name == "." {
			return nil
		}
		if !fs.ValidPath(name) {
			return fmt.Errorf("validate cached GitHub tree path %q: invalid relative slash path", name)
		}
		if err := validateSafePath(name); err != nil {
			return fmt.Errorf("validate cached GitHub tree path %q: %w", name, err)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat cached GitHub tree entry %q: %w", name, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("validate cached GitHub tree entry %q: symlinks are not allowed", name)
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("validate cached GitHub tree entry %q: unsupported file type", name)
		}
		if name == "registry.json" && info.Mode().IsRegular() {
			registryFound = true
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("verify cached GitHub tree %q: %w", treePath, err)
	}
	if !registryFound {
		return fmt.Errorf("verify cached GitHub tree %q: required registry.json is missing", treePath)
	}
	return nil
}

func extractGitHubArchive(ctx context.Context, archivePath, treePath string) (err error) {
	archiveFile, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open staged GitHub archive %q: %w", archivePath, err)
	}
	defer closeSource(&err, archiveFile, "staged GitHub archive "+archivePath)

	gzipReader, err := gzip.NewReader(archiveFile)
	if err != nil {
		return fmt.Errorf("open gzip stream in GitHub archive %q: %w", archivePath, err)
	}
	defer closeSource(&err, gzipReader, "GitHub archive gzip stream "+archivePath)

	if err := os.Mkdir(treePath, 0o700); err != nil {
		return fmt.Errorf("create staged GitHub tree %q: %w", treePath, err)
	}

	tarReader := tar.NewReader(&sourceContextReader{ctx: ctx, reader: gzipReader})
	var archiveRoot string
	rootEntrySeen := false
	var expanded int64
	entries := 0
	seen := make(map[string]struct{})

	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("extract GitHub archive %q: %w", archivePath, err)
		}
		header, nextErr := tarReader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return fmt.Errorf("read tar entry from GitHub archive %q: %w", archivePath, nextErr)
		}
		if header.Typeflag == tar.TypeXGlobalHeader || header.Typeflag == tar.TypeXHeader {
			continue
		}
		entries++
		if entries > maxGitHubEntries {
			return fmt.Errorf("extract GitHub archive %q: entry count exceeds %d", archivePath, maxGitHubEntries)
		}

		root, name, pathErr := githubArchivePath(header.Name, header.Typeflag)
		if pathErr != nil {
			return fmt.Errorf("archive path %q: %w", header.Name, pathErr)
		}
		if archiveRoot == "" {
			archiveRoot = root
		} else if archiveRoot != root {
			return fmt.Errorf("archive path %q: root %q differs from archive root %q", header.Name, root, archiveRoot)
		}
		if name == "" {
			if header.Typeflag != tar.TypeDir {
				return fmt.Errorf("archive path %q: root entry must be a directory", header.Name)
			}
			if rootEntrySeen {
				return fmt.Errorf("archive path %q: duplicate archive root entry", header.Name)
			}
			rootEntrySeen = true
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("archive path %q: duplicate extracted path %q", header.Name, name)
		}
		seen[name] = struct{}{}

		destination := filepath.Join(treePath, filepath.FromSlash(name))
		switch header.Typeflag {
		case tar.TypeDir:
			if header.Size != 0 {
				return fmt.Errorf("archive path %q: directory has nonzero size", header.Name)
			}
			if err := os.MkdirAll(destination, 0o700); err != nil {
				return fmt.Errorf("create archive directory %q: %w", name, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 {
				return fmt.Errorf("archive path %q: regular file has negative size", header.Name)
			}
			if header.Size > maxGitHubExpanded-expanded {
				return fmt.Errorf("archive path %q: expanded data exceeds %d-byte limit", header.Name, maxGitHubExpanded)
			}
			if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
				return fmt.Errorf("create parent directory for archive path %q: %w", name, err)
			}
			if err := extractGitHubRegularFile(ctx, tarReader, destination, name, header.Size); err != nil {
				return err
			}
			expanded += header.Size
		case tar.TypeSymlink:
			return fmt.Errorf("archive path %q: symbolic links are not allowed", header.Name)
		case tar.TypeLink:
			return fmt.Errorf("archive path %q: hard links are not allowed", header.Name)
		case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
			return fmt.Errorf("archive path %q: device and FIFO entries are not allowed", header.Name)
		default:
			return fmt.Errorf("archive path %q: unsupported tar entry type %d", header.Name, header.Typeflag)
		}
	}
	if archiveRoot == "" {
		return fmt.Errorf("extract GitHub archive %q: archive is empty", archivePath)
	}
	return nil
}

func extractGitHubRegularFile(ctx context.Context, tarReader io.Reader, destination, archiveName string, size int64) (err error) {
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create archive file %q: %w", archiveName, err)
	}
	defer closeSource(&err, file, "archive file "+archiveName)

	written, err := io.Copy(file, &sourceContextReader{ctx: ctx, reader: tarReader})
	if err != nil {
		return fmt.Errorf("write archive file %q: %w", archiveName, err)
	}
	if written != size {
		return fmt.Errorf("write archive file %q: wrote %d bytes, expected %d", archiveName, written, size)
	}
	return nil
}

func githubArchivePath(raw string, typeflag byte) (string, string, error) {
	if raw == "" || strings.HasPrefix(raw, "/") || strings.Contains(raw, "\\") {
		return "", "", fmt.Errorf("must be a relative slash path")
	}
	for _, character := range []byte(raw) {
		if character < 0x20 || character == 0x7f {
			return "", "", fmt.Errorf("contains a control character")
		}
	}

	parts := strings.Split(raw, "/")
	if parts[len(parts)-1] == "" {
		if typeflag != tar.TypeDir {
			return "", "", fmt.Errorf("only directory paths may end in a slash")
		}
		parts = parts[:len(parts)-1]
	}
	if len(parts) == 0 {
		return "", "", fmt.Errorf("archive root is empty")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", "", fmt.Errorf("contains an empty, current, or parent path segment")
		}
	}
	if err := validateSafePath(parts[0]); err != nil {
		return "", "", fmt.Errorf("unsafe archive root %q: %w", parts[0], err)
	}
	if len(parts) == 1 {
		return parts[0], "", nil
	}

	name := strings.Join(parts[1:], "/")
	if !fs.ValidPath(name) {
		return "", "", fmt.Errorf("stripped path %q is not a valid relative slash path", name)
	}
	if err := validateSafePath(name); err != nil {
		return "", "", fmt.Errorf("unsafe stripped path %q: %w", name, err)
	}
	return parts[0], name, nil
}

func validGitHubCommit(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, character := range []byte(value) {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func readGitHubErrorResponse(response *http.Response) (text string, err error) {
	defer closeSource(&err, response.Body, "GitHub error response body")
	body, err := io.ReadAll(io.LimitReader(response.Body, maxGitHubErrorBody+1))
	if err != nil {
		return "", fmt.Errorf("read GitHub error response body: %w", err)
	}
	truncated := int64(len(body)) > maxGitHubErrorBody
	if truncated {
		body = body[:maxGitHubErrorBody]
	}
	text = strings.TrimSpace(string(body))
	if text == "" {
		return "<empty response body>", nil
	}
	if truncated {
		text += " [truncated]"
	}
	return text, nil
}

func readAndCloseBounded(body io.ReadCloser, limit int64, description string) (data []byte, err error) {
	defer closeSource(&err, body, description+" body")
	data, err = io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s body: %w", description, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("read %s body: exceeds %d-byte limit", description, limit)
	}
	return data, nil
}

func closeSource(destination *error, closer io.Closer, description string) {
	if err := closer.Close(); err != nil {
		*destination = errors.Join(*destination, fmt.Errorf("close %s: %w", description, err))
	}
}
