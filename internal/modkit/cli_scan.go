package modkit

import (
	"fmt"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strings"
)

// cliHandlerBannedImports names the packages a contributed CLI handler may
// never import. Handlers receive a gggcli.CommandContext whose only route
// back into the project is Controller operations: provider SDK calls,
// network traffic, and process execution belong in the module's declared
// provisioner, database-operator, and deployer packages, which the registry
// constructs and the CLI invokes — never in a command handler that would
// bypass the plan/preview boundary or hold a secret reader.
var cliHandlerBannedImports = map[string]string{
	"net/http":        "network calls belong in the module's provisioner or deployer package",
	"os/exec":         "process execution belongs in the module's declared provisioner, database operator, or deployer package",
	"internal/remote": "provider and deploy clients are reached through the typed registries, not command handlers",
}

// ValidateCLIHandlerPackages scans every module's contributed command
// handler sources for banned imports. Files are the installed payload bytes
// keyed by target path, so the scan runs over exactly the bytes sync would
// write — including rewritten import paths.
func ValidateCLIHandlerPackages(modules []Manifest, files map[string][]byte) error {
	for _, module := range modules {
		if len(module.Runtime.CLI) == 0 {
			continue
		}
		// This module's own execution seams: their packages are where its
		// provider mutation belongs, which is exactly what a handler must
		// not short-circuit around.
		seams := map[string]string{}
		for _, provisioner := range module.Runtime.Provisioners {
			seams[provisioner.Package] = fmt.Sprintf("provisioner %s", provisioner.ID)
		}
		for _, operator := range module.Runtime.DatabaseOps {
			seams[operator.Package] = fmt.Sprintf("database operator %s", operator.ID)
		}
		for _, deployer := range module.Runtime.Deploy {
			seams[deployer.Package] = fmt.Sprintf("deployer %s", deployer.ID)
		}
		for _, command := range module.Runtime.CLI {
			dir := path.Clean("/" + command.Package)
			prefix := strings.TrimPrefix(dir, "/")
			for _, file := range module.Files {
				if file.Class == FileClassGenerated {
					continue
				}
				target := path.Clean("/" + file.Target)
				if path.Dir(target) != dir {
					continue
				}
				content, ok := files[strings.TrimPrefix(target, "/")]
				if !ok {
					continue
				}
				if err := scanHandlerImports(module.ID, command.Name, prefix, content, seams); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func scanHandlerImports(moduleID, command, packagePrefix string, content []byte, seams map[string]string) error {
	parsed, err := parser.ParseFile(token.NewFileSet(), packagePrefix+".go", content, parser.ImportsOnly)
	if err != nil {
		return fmt.Errorf("module %s command %s: parse %s: %w", moduleID, command, packagePrefix, err)
	}
	for _, imported := range parsed.Imports {
		raw := strings.Trim(imported.Path.Value, `"`)
		// A path with a leading dot segment is relative after rewriting;
		// match on the last segments so vendor-prefixed and module-relative
		// forms are both caught.
		banned, reason := matchBannedImport(raw, seams)
		if reason != "" {
			return fmt.Errorf(
				"module %s command %s handler imports %s: %s; command handlers may reach the project only through the gggcli controller",
				moduleID, command, raw, reason,
			)
		}
		_ = banned
	}
	return nil
}

func matchBannedImport(raw string, seams map[string]string) (string, string) {
	for banned, reason := range cliHandlerBannedImports {
		if raw == banned || strings.HasSuffix(raw, "/"+banned) {
			return banned, reason
		}
	}
	for seam, label := range seams {
		if raw == seam || strings.HasSuffix(raw, "/"+seam) || strings.HasSuffix(seam, "/"+raw) {
			return raw, fmt.Sprintf("the %s package owns provider mutation", label)
		}
	}
	return "", ""
}

// coreCLIPackagePrefix is the core CLI presentation tree. Nothing under it may
// name an adapter package.
const coreCLIPackagePrefix = "internal/gggcli/"

// ValidateCoreCLIPackages refuses an adapter-owned package import anywhere
// under internal/gggcli. The CLI reaches a selected adapter only through the
// generated per-slot accessors in internal/modules, which are the sole thing
// that names an adapter package; a direct import pins an unselected adapter
// into every build, which is exactly what made a project that does not select
// ggg/system/identity-clerk impossible to compile. The scan is
// direct-import-only, which is the right shape here: the defect prevented IS a
// direct import, and the generated accessor is the sanctioned indirection.
//
// Every package an adapter CLAIMS is banned, not only its constructor package.
// An adapter's claims are exclusive and leave with it, so importing any of them
// pins the adapter just as hard — internal/identity/clerkurl, the leaf the
// generated config loader calls, is exactly such a package and would otherwise
// have passed.
func ValidateCoreCLIPackages(modules []Manifest, files map[string][]byte) error {
	adapters := map[string]string{}
	for _, module := range modules {
		sys := module.Runtime.System
		if sys == nil || sys.Adapter == nil {
			continue
		}
		if sys.Package != "" {
			adapters[strings.Trim(sys.Package, "/")] = module.ID
		}
		for _, claimed := range module.Claims.Packages {
			if trimmed := strings.Trim(claimed, "/"); trimmed != "" {
				adapters[trimmed] = module.ID
			}
		}
	}
	if len(adapters) == 0 {
		return nil
	}
	targets := make([]string, 0, len(files))
	for target := range files {
		if strings.HasPrefix(target, coreCLIPackagePrefix) && strings.HasSuffix(target, ".go") {
			targets = append(targets, target)
		}
	}
	sort.Strings(targets)
	for _, target := range targets {
		parsed, err := parser.ParseFile(token.NewFileSet(), target, files[target], parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("scan %s: %w", target, err)
		}
		for _, spec := range parsed.Imports {
			raw := strings.Trim(spec.Path.Value, "\"`")
			for pkg, id := range adapters {
				if raw != pkg && !strings.HasSuffix(raw, "/"+pkg) {
					continue
				}
				return fmt.Errorf(
					"%s imports adapter package %s owned by %s; the CLI must reach a selected adapter through the generated per-slot accessors in internal/modules, so an unselected adapter stays out of the build",
					target, pkg, id)
			}
		}
	}
	return nil
}
