package modkit

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

// ToolRunner executes a fixed argv in a project root; manifests never carry
// shell fragments.
type ToolRunner interface {
	Run(context.Context, string, []string) error
}

type OSCommandRunner struct{}

func (OSCommandRunner) Run(ctx context.Context, root string, argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("tool argv is empty")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = root
	cmd.Env = os.Environ()
	return cmd.Run()
}

// EffectiveDependencies chooses the greatest declared version per module path
// and rejects conflicting immutable tool/container metadata.
func EffectiveDependencies(modules []Manifest) (Dependencies, error) {
	var out Dependencies
	out.Go = []GoDependency{}
	out.Tools = []ToolArtifact{}
	out.Containers = []ContainerDependency{}
	goVersions := map[string]string{}
	for _, m := range modules {
		for _, d := range m.Dependencies.Go {
			if err := module.CheckPath(d.Module); err != nil {
				return Dependencies{}, fmt.Errorf("%s: %w", d.Module, err)
			}
			if !semver.IsValid(d.Version) {
				return Dependencies{}, fmt.Errorf("%s: invalid semantic version %q", d.Module, d.Version)
			}
			if err := module.Check(d.Module, d.Version); err != nil {
				return Dependencies{}, fmt.Errorf("%s: %w", d.Module, err)
			}
			if old, ok := goVersions[d.Module]; !ok || semver.Compare(old, d.Version) < 0 {
				goVersions[d.Module] = d.Version
			}
		}
		out.Tools = append(out.Tools, m.Dependencies.Tools...)
		out.Containers = append(out.Containers, m.Dependencies.Containers...)
	}
	for p, v := range goVersions {
		out.Go = append(out.Go, GoDependency{Module: p, Version: v})
	}
	sort.Slice(out.Go, func(i, j int) bool { return out.Go[i].Module < out.Go[j].Module })
	if err := mergeTools(out.Tools); err != nil {
		return Dependencies{}, err
	}
	if err := mergeContainers(out.Containers); err != nil {
		return Dependencies{}, err
	}
	return out, nil
}
func mergeTools(all []ToolArtifact) error {
	seen := map[string]ToolArtifact{}
	for _, t := range all {
		if old, ok := seen[t.InstallPath]; ok && old != t {
			return fmt.Errorf("conflicting tool artifact %q", t.InstallPath)
		}
		seen[t.InstallPath] = t
	}
	return nil
}
func mergeContainers(all []ContainerDependency) error {
	seen := map[string]string{}
	for _, c := range all {
		if old, ok := seen[c.Name]; ok && old != c.Image {
			return fmt.Errorf("conflicting container %q", c.Name)
		}
		seen[c.Name] = c.Image
	}
	return nil
}

// UpdateGoMod journals managed dependency ownership and rolls go.mod/go.sum
// back when the injected tool runner fails. Existing requirements become
// preexisting dependencies and are never removed unless their recorded managed
// version is still current.
func UpdateGoMod(ctx context.Context, root string, deps []LockedDependency, runner ToolRunner) ([]LockedDependency, error) {
	return reconcileGoMod(ctx, root, nil, deps, runner)
}

func reconcileGoMod(ctx context.Context, root string, previous, desired []LockedDependency, runner ToolRunner) ([]LockedDependency, error) {
	modPath := filepath.Join(root, "go.mod")
	sumPath := filepath.Join(root, "go.sum")
	oldMod, err := os.ReadFile(modPath)
	if err != nil {
		return nil, err
	}
	oldSum, sumErr := os.ReadFile(sumPath)
	if sumErr != nil && !os.IsNotExist(sumErr) {
		return nil, sumErr
	}
	file, err := modfile.Parse("go.mod", oldMod, nil)
	if err != nil {
		return nil, err
	}
	previousBy := make(map[string]LockedDependency, len(previous))
	for _, dep := range previous {
		previousBy[dep.Module] = dep
	}
	next := make([]LockedDependency, 0, len(desired))
	for _, dep := range desired {
		current := findRequire(file, dep.Module)
		prior, hadPrior := previousBy[dep.Module]
		if hadPrior {
			dep.Preexisting = prior.Preexisting
			dep.BaselineVersion = prior.BaselineVersion
			if len(dep.Owners) == 0 {
				dep.Owners = append([]string{}, prior.Owners...)
			}
		} else if current != nil {
			dep.Preexisting = true
			dep.BaselineVersion = current.Mod.Version
		}
		if current == nil {
			if err := file.AddRequire(dep.Module, dep.ManagedVersion); err != nil {
				return nil, err
			}
		} else if !hadPrior || current.Mod.Version == prior.ManagedVersion {
			// Only move an unchanged managed requirement. A user edit is
			// deliberately preserved as user-owned.
			if current.Mod.Version != dep.ManagedVersion {
				if err := file.AddRequire(dep.Module, dep.ManagedVersion); err != nil {
					return nil, err
				}
			}
		}
		next = append(next, dep)
	}
	desiredBy := make(map[string]struct{}, len(desired))
	for _, dep := range desired {
		desiredBy[dep.Module] = struct{}{}
	}
	for _, prior := range previous {
		if _, keep := desiredBy[prior.Module]; keep {
			continue
		}
		current := findRequire(file, prior.Module)
		if current == nil || current.Mod.Version != prior.ManagedVersion {
			continue
		}
		if prior.Preexisting {
			if prior.BaselineVersion == "" {
				continue
			}
			if err := file.AddRequire(prior.Module, prior.BaselineVersion); err != nil {
				return nil, err
			}
		} else {
			file.DropRequire(prior.Module)
		}
	}
	formatted, err := file.Format()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(modPath, formatted, 0o644); err != nil {
		return nil, err
	}
	if runner != nil {
		if err := runner.Run(ctx, root, []string{"go", "mod", "download"}); err != nil {
			_ = os.WriteFile(modPath, oldMod, 0o644)
			if sumErr == nil {
				_ = os.WriteFile(sumPath, oldSum, 0o644)
			} else {
				_ = os.Remove(sumPath)
			}
			return nil, err
		}
	}
	return next, nil
}

func findRequire(file *modfile.File, modulePath string) *modfile.Require {
	for _, req := range file.Require {
		if req.Mod.Path == modulePath {
			return req
		}
	}
	return nil
}

// ExtractTool verifies an artifact digest before decoding it and rejects
// traversal, links, extra files, and destinations outside bin/.
func ExtractTool(data []byte, artifact ToolArtifact, root string) error {
	if !strings.HasPrefix(artifact.URL, "https://") || !validSHA256(artifact.SHA256) {
		return fmt.Errorf("invalid tool artifact metadata")
	}
	if artifact.Format != "raw" && artifact.Format != "zip" && artifact.Format != "tar.gz" {
		return fmt.Errorf("unsupported tool format %q", artifact.Format)
	}
	if !validSafeArchivePath(artifact.BinaryPath) || !validSafeInstallPath(artifact.InstallPath) {
		return fmt.Errorf("unsafe tool path")
	}
	dst := filepath.Join(root, filepath.FromSlash(artifact.InstallPath))
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	cleanDst, err := filepath.Abs(dst)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(cleanRoot, cleanDst)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("tool destination escapes project root")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != artifact.SHA256 {
		return fmt.Errorf("tool digest mismatch")
	}
	var payload []byte
	switch artifact.Format {
	case "raw":
		payload = data
	case "zip":
		z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return err
		}
		for _, f := range z.File {
			if !validSafeArchivePath(filepath.ToSlash(f.Name)) {
				return fmt.Errorf("unsafe tool archive path %q", f.Name)
			}
			if f.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("tool symlink rejected")
			}
			if f.Name == artifact.BinaryPath {
				if f.FileInfo().IsDir() {
					return fmt.Errorf("tool binary is a directory")
				}
				r, err := f.Open()
				if err != nil {
					return err
				}
				payload, err = io.ReadAll(r)
				_ = r.Close()
				if err != nil {
					return err
				}
				continue
			}
			if !f.FileInfo().IsDir() && f.Mode()&0111 != 0 {
				return fmt.Errorf("undeclared tool executable %q", f.Name)
			}
		}
	case "tar.gz":
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return err
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if !validSafeArchivePath(filepath.ToSlash(h.Name)) {
				return fmt.Errorf("unsafe tool archive path %q", h.Name)
			}
			if h.Typeflag == tar.TypeSymlink || h.Typeflag == tar.TypeLink || h.Typeflag == tar.TypeBlock || h.Typeflag == tar.TypeChar || h.Typeflag == tar.TypeFifo {
				return fmt.Errorf("tool link/device rejected")
			}
			if h.Name == artifact.BinaryPath {
				if h.Typeflag != tar.TypeReg {
					return fmt.Errorf("tool non-regular file rejected")
				}
				payload, err = io.ReadAll(tr)
				if err != nil {
					return err
				}
				continue
			}
			if h.Typeflag == tar.TypeReg && h.Mode&0111 != 0 {
				return fmt.Errorf("undeclared tool executable %q", h.Name)
			}
		}
	}
	if payload == nil {
		return fmt.Errorf("declared tool executable %q not found", artifact.BinaryPath)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, payload, 0o755)
}

// ValidateDeclaredImports rejects external imports without a manifest owner.
// generated contains rendered Go source, not paths.
func ValidateDeclaredImports(files map[string][]byte, generated []string, declared []GoDependency) error {
	allowed := map[string]bool{}
	for _, dep := range declared {
		allowed[dep.Module] = true
	}
	scan := func(name string, data []byte) error {
		if !strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, ".templ") {
			return nil
		}
		paths := []string{}
		if strings.HasSuffix(name, ".templ") {
			for _, clause := range regexp.MustCompile(`(?s)\bimport\s*(?:\([^)]*\)|"[^"]+")`).FindAllString(string(data), -1) {
				for _, match := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(clause, -1) {
					if len(match) > 1 {
						paths = append(paths, match[1])
					}
				}
			}
		} else {
			f, err := parser.ParseFile(token.NewFileSet(), name, data, parser.ImportsOnly)
			if err != nil {
				return fmt.Errorf("parse %s: %w", name, err)
			}
			for _, imp := range f.Imports {
				paths = append(paths, strings.Trim(imp.Path.Value, "\""))
			}
		}
		for _, path := range paths {
			if strings.Contains(path, ".") && !hasDependencyPrefix(path, allowed) {
				return fmt.Errorf("undeclared direct dependency %q imported by %s", path, name)
			}
		}
		return nil
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := scan(name, files[name]); err != nil {
			return err
		}
	}
	for i, source := range generated {
		if err := scan(fmt.Sprintf("generated_%d.go", i), []byte(source)); err != nil {
			return err
		}
	}
	return nil
}

// ValidateModuleDeclaredImports applies dependency ownership to one module's
// authored files. A module may import the project/core module and only the
// third-party modules it declares; dependencies declared by a sibling module
// are intentionally not ambient.
func ValidateModuleDeclaredImports(moduleID string, files map[string][]byte, generated []string, declared []GoDependency) error {
	if err := ValidateDeclaredImports(files, generated, declared); err != nil {
		return fmt.Errorf("module %s: %w", moduleID, err)
	}
	return nil
}

func hasDependencyPrefix(path string, allowed map[string]bool) bool {
	for modulePath := range allowed {
		if path == modulePath || strings.HasPrefix(path, modulePath+"/") {
			return true
		}
	}
	return false
}

// ReconcileManagedDependencies applies owner-removal rules to go.mod. A
// requirement changed after the previous sync is user-owned and is never
// lowered or removed by module removal.
func ReconcileManagedDependencies(ctx context.Context, root string, previous, desired []LockedDependency, runner ToolRunner) ([]LockedDependency, error) {
	return reconcileGoMod(ctx, root, previous, desired, runner)
}
