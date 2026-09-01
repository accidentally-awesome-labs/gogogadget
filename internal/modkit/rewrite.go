package modkit

import (
	"fmt"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type byteReplacement struct {
	start int
	end   int
	value []byte
}

func rewriteModuleImports(name string, content []byte, canonical, target string) ([]byte, error) {
	return rewriteModuleImportsForPrefixes(name, content, []string{canonical}, target)
}

func rewriteModuleImportsForPrefixes(name string, content []byte, canonicals []string, target string) ([]byte, error) {
	extension := strings.ToLower(filepath.Ext(name))
	if extension != ".go" && extension != ".templ" {
		return nil, fmt.Errorf("rewrite_module is unsupported for %s", name)
	}
	if !validPackagePath(target) || len(canonicals) == 0 {
		return nil, fmt.Errorf("module rewrite paths are invalid")
	}
	for _, canonical := range canonicals {
		if !validPackagePath(canonical) {
			return nil, fmt.Errorf("module rewrite paths are invalid")
		}
	}
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, name, content, parser.ImportsOnly)
	if err != nil {
		return nil, fmt.Errorf("parse imports in %s: %w", name, err)
	}
	replacements := make([]byteReplacement, 0, len(parsed.Imports))
	for _, spec := range parsed.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, fmt.Errorf("parse import path in %s: %w", name, err)
		}
		canonical := ""
		for _, prefix := range canonicals {
			if importPath == prefix || strings.HasPrefix(importPath, prefix+"/") {
				if len(prefix) > len(canonical) {
					canonical = prefix
				}
			}
		}
		if canonical == "" {
			continue
		}
		rewritten := target + strings.TrimPrefix(importPath, canonical)
		start := files.Position(spec.Path.Pos()).Offset
		end := files.Position(spec.Path.End()).Offset
		if start < 0 || end < start || end > len(content) {
			return nil, fmt.Errorf("invalid import position in %s", name)
		}
		replacements = append(replacements, byteReplacement{start: start, end: end, value: []byte(strconv.Quote(rewritten))})
	}
	result := append([]byte(nil), content...)
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start > replacements[j].start })
	for _, replacement := range replacements {
		updated := make([]byte, 0, len(result)-(replacement.end-replacement.start)+len(replacement.value))
		updated = append(updated, result[:replacement.start]...)
		updated = append(updated, replacement.value...)
		updated = append(updated, result[replacement.end:]...)
		result = updated
	}
	return result, nil
}
