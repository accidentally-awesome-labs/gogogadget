package modkit

import (
	"fmt"
	"path/filepath"
)

// ProjectLocalRegistry returns the only mutable registry source allowed for a
// project. The namespace is the validated project slug, and the path is
// deliberately relative so it cannot escape the project root.
func ProjectLocalRegistry(root, slug string) (ProjectRegistry, error) {
	if !validNamespace(slug) {
		return ProjectRegistry{}, fmt.Errorf("project slug %q is not a valid registry namespace", slug)
	}
	if root == "" {
		return ProjectRegistry{}, fmt.Errorf("project root is required")
	}
	return ProjectRegistry{Namespace: slug, Source: "directory", Path: filepath.ToSlash(filepath.Join("registry"))}, nil
}
