package modkit

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type lockedOwner struct {
	module string
	base   string
}

func validateLockOwnership(lock Lock, present bool) error {
	if !present {
		return nil
	}
	owners := make(map[string]string)
	for _, module := range lock.Modules {
		for _, file := range module.Files {
			if previous, exists := owners[file.Path]; exists {
				return fmt.Errorf("lock target ownership collision for %q between %s and %s", file.Path, previous, module.ID)
			}
			owners[file.Path] = module.ID
		}
	}
	return nil
}

func lockedFileOwnership(lock Lock, present bool) map[string]lockedOwner {
	owners := make(map[string]lockedOwner)
	if !present {
		return owners
	}
	for _, module := range lock.Modules {
		for _, file := range module.Files {
			owners[file.Path] = lockedOwner{module: module.ID, base: file.BaseSHA256}
		}
	}
	return owners
}

// graphFileOwnership names the module that owns each authored target in the
// selected graph. Exclusivity is not re-checked here: preflightNamespaces
// already refuses a target two selected modules both claim ("target namespace
// %q collision"), and it runs before reconciliation on the single planning
// path. That guarantee is what makes the transfer rule below unambiguous.
func graphFileOwnership(modules []Manifest) map[string]string {
	owners := make(map[string]string, len(modules))
	for _, module := range modules {
		for _, file := range module.Files {
			owners[file.Target] = module.ID
		}
	}
	return owners
}

// applyOwnershipTransfers hands lock ownership to a target's new owner when
// the catalog moved it between modules in this same plan. Moving a file's
// owner is a manifest edit, not a file move, so without this every derivative
// that already synced the old owner would refuse forever with no migration
// path.
//
// It is safe because the new claim is exclusive: a differing owner therefore
// means the previous one dropped the target in this plan. The recorded base
// digest is carried over untouched, so a locally modified file is still
// refused by classifyAuthoredTarget rather than silently overwritten by its
// new owner.
func applyOwnershipTransfers(owners map[string]lockedOwner, graphOwners map[string]string) map[string]lockedOwner {
	for target, owner := range owners {
		next, claimed := graphOwners[target]
		if !claimed || next == owner.module {
			continue
		}
		owners[target] = lockedOwner{module: next, base: owner.base}
	}
	return owners
}

func classifyAuthoredTarget(root, module string, file ManifestFile, content []byte, owners map[string]lockedOwner) (Change, error) {
	info, missing, err := lstatProjectPath(root, file.Target)
	if err != nil {
		return Change{}, fmt.Errorf("target %s: %w", file.Target, err)
	}
	change := Change{
		Path: file.Target, Module: module, Source: file.Source, Class: DestinationAuthored,
		SHA256: digestBytes(content), Content: append([]byte(nil), content...),
	}
	owner, owned := owners[file.Target]
	if owned && owner.module != module {
		return Change{}, fmt.Errorf("target %s is owned by %s, not %s", file.Target, owner.module, module)
	}
	if missing {
		change.Kind = ChangeCreate
		return change, nil
	}
	if !info.Mode().IsRegular() {
		return Change{}, fmt.Errorf("target %s is not a regular file", file.Target)
	}
	current, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file.Target)))
	if err != nil {
		return Change{}, fmt.Errorf("read target %s: %w", file.Target, err)
	}
	currentDigest := digestBytes(current)
	if currentDigest == change.SHA256 {
		change.Kind = ChangeUnchanged
		return change, nil
	}
	if !owned {
		return Change{}, fmt.Errorf("target %s differs from planned bytes and is unowned", file.Target)
	}
	if currentDigest != owner.base {
		return Change{}, fmt.Errorf("target %s owned by %s is locally modified", file.Target, module)
	}
	change.Kind = ChangeUpdate
	return change, nil
}

func classifyOwnedTarget(root, target, module, source string, class DestinationClass, content []byte, allowUpdate bool) (Change, error) {
	info, missing, err := lstatProjectPath(root, target)
	if err != nil {
		return Change{}, fmt.Errorf("target %s: %w", target, err)
	}
	change := Change{
		Path: target, Module: module, Source: source, Class: class,
		SHA256: digestBytes(content), Content: append([]byte(nil), content...),
	}
	if missing {
		change.Kind = ChangeCreate
		return change, nil
	}
	if !info.Mode().IsRegular() {
		return Change{}, fmt.Errorf("target %s is not a regular file", target)
	}
	current, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(target)))
	if err != nil {
		return Change{}, fmt.Errorf("read target %s: %w", target, err)
	}
	if digestBytes(current) == change.SHA256 {
		change.Kind = ChangeUnchanged
		return change, nil
	}
	if !allowUpdate {
		return Change{}, fmt.Errorf("immutable target %s differs from its verified payload", target)
	}
	change.Kind = ChangeUpdate
	return change, nil
}

func lstatProjectPath(root, target string) (fs.FileInfo, bool, error) {
	if err := validateSafePath(target); err != nil {
		return nil, false, err
	}
	segments := strings.Split(target, "/")
	current := root
	for i, segment := range segments {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, true, nil
		}
		if err != nil {
			return nil, false, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, false, fmt.Errorf("path component %s is a symlink", strings.Join(segments[:i+1], "/"))
		}
		if i < len(segments)-1 && !info.IsDir() {
			return nil, false, fmt.Errorf("path component %s is not a directory", strings.Join(segments[:i+1], "/"))
		}
		if i == len(segments)-1 {
			return info, false, nil
		}
	}
	return nil, true, nil
}

func digestBytes(content []byte) string {
	sum := sha256.Sum256(content)
	return fmt.Sprintf("%x", sum)
}
