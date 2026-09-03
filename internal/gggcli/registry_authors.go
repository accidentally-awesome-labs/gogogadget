package gggcli

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// The registry authoring subcommands (`ggg registry init|keygen|sign|verify|
// rotate`) are fixed filesystem operations on a registry tree, like `registry
// build`: they run through their own journal-free path because they author a
// registry, not a project. The project-sourced flows (`add|remove|update`)
// preview and apply through the one planned-transaction path every other
// mutation uses.

// registryDirFlag resolves the registry tree a subcommand acts on: an
// explicit --dir, else the project's directory registry, else the project
// root itself (the self-hosting layout).
func (c *Controller) registryDirFlag(dir string) string {
	if dir != "" {
		return dir
	}
	if project, err := c.loadProject(); err == nil {
		for _, registry := range project.Registries {
			if registry.Source == "directory" && registry.Path != "" {
				return registry.Path
			}
		}
	}
	if _, err := os.Stat("registry.json"); err == nil {
		return "."
	}
	return "."
}

// registryBuildDir resolves the registry tree `ggg registry build` rebuilds.
// Absent --dir it is the project root, which is the self-hosting layout every
// existing caller relies on. An explicit --dir is project-relative — a
// publisher building a registry that is not the project root, and this
// repository rebuilding the external-registry template it ships — and may not
// escape the root, because build rewrites manifests and indexes in place.
func (c *Controller) registryBuildDir(dir string) (string, error) {
	root := c.rootDir()
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		return root, nil
	}
	if filepath.IsAbs(trimmed) || trimmed != dir || strings.Contains(trimmed, "..") {
		return "", usageError("registry build --dir must be a project-relative path")
	}
	return filepath.Join(root, filepath.FromSlash(trimmed)), nil
}

func runRegistryInit(cc CommandContext, parsed parsedArgs) (Result, error) {
	namespace := parsed.value("namespace", "")
	canonical := parsed.value("canonical-module", "")
	dir := cc.Controller.registryDirFlag(parsed.value("dir", ""))
	if namespace == "" || canonical == "" {
		return Result{}, usageError("registry init requires --namespace and --canonical-module")
	}
	written, err := modkit.InitRegistryTree(dir, namespace, canonical)
	if err != nil {
		return failureEnvelope("registry init", refusalError(err))
	}
	if written == nil {
		written = []string{}
	}
	return Result{
		Envelope: normalizeEnvelope(modkit.Envelope{
			Command: "registry init", OK: true, Exit: exitOK, Generated: written,
		}),
		Payload: map[string]any{"written": written, "dir": dir, "namespace": namespace},
	}, nil
}

func runRegistryKeygen(parsed parsedArgs) (Result, error) {
	privatePath := parsed.value("private", "")
	publicPath := parsed.value("public", "")
	fingerprint, err := modkit.GenerateRegistryKeyPair(privatePath, publicPath)
	if err != nil {
		return failureEnvelope("registry keygen", refusalError(err))
	}
	return Result{
		Envelope: normalizeEnvelope(modkit.Envelope{
			Command: "registry keygen", OK: true, Exit: exitOK, Resolved: []string{fingerprint},
		}),
		Payload: map[string]any{
			"fingerprint": fingerprint, "private": privatePath, "public": publicPath,
		},
	}, nil
}

func runRegistrySign(cc CommandContext, parsed parsedArgs) (Result, error) {
	dir := cc.Controller.registryDirFlag(parsed.value("dir", ""))
	keyFile := parsed.value("key-file", "")
	envKey := os.Getenv(modkit.RegistryPrivateKeyEnv)
	if (keyFile == "") == (envKey == "") {
		return Result{}, usageError("registry sign accepts exactly one of --key-file or " + modkit.RegistryPrivateKeyEnv)
	}
	private, err := loadSigningKey(keyFile, envKey)
	if err != nil {
		return failureEnvelope("registry sign", refusalError(err))
	}
	data, err := modkit.SignRegistrySnapshot(dir, private)
	if err != nil {
		return failureEnvelope("registry sign", runtimeError(err))
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	return Result{
		Envelope: normalizeEnvelope(modkit.Envelope{
			Command: "registry sign", OK: true, Exit: exitOK,
			Generated: []string{modkit.RegistrySnapshotPath, modkit.RegistrySignaturePath},
		}),
		Payload: map[string]any{"digest": digest, "dir": dir},
	}, nil
}

func loadSigningKey(keyFile, envKey string) (ed25519.PrivateKey, error) {
	if keyFile != "" {
		return modkit.LoadRegistryPrivateKey(keyFile)
	}
	return modkit.RegistryPrivateKeyFromEnv(envKey)
}

func runRegistryVerify(cc CommandContext, parsed parsedArgs) (Result, error) {
	dir := cc.Controller.registryDirFlag(parsed.value("dir", ""))
	publicKey := parsed.value("public-key", "")
	if publicKey == "" {
		return Result{}, usageError("registry verify requires --public-key")
	}
	digest, err := modkit.VerifyRegistrySnapshot(dir, publicKey)
	if err != nil {
		return failureEnvelope("registry verify", refusalError(err))
	}
	return Result{
		Envelope: normalizeEnvelope(modkit.Envelope{
			Command: "registry verify", OK: true, Exit: exitOK, Resolved: []string{digest},
		}),
		Payload: map[string]any{"digest": digest, "dir": dir},
	}, nil
}

func runRegistryRotate(cc CommandContext, parsed parsedArgs) (Result, error) {
	dir := cc.Controller.registryDirFlag(parsed.value("dir", ""))
	oldPath := parsed.value("old-key-file", "")
	newPath := parsed.value("new-key-file", "")
	notBefore := parsed.value("not-before", "")
	if oldPath == "" || newPath == "" || notBefore == "" {
		return Result{}, usageError("registry rotate requires --old-key-file, --new-key-file, and --not-before")
	}
	oldKey, err := modkit.LoadRegistryPrivateKey(oldPath)
	if err != nil {
		return failureEnvelope("registry rotate", refusalError(err))
	}
	newKey, err := modkit.LoadRegistryPrivateKey(newPath)
	if err != nil {
		return failureEnvelope("registry rotate", refusalError(err))
	}
	if err := modkit.WriteRegistryKeyRotation(dir, oldKey, newKey, notBefore); err != nil {
		return failureEnvelope("registry rotate", refusalError(err))
	}
	newPublic, ok := newKey.Public().(ed25519.PublicKey)
	if !ok {
		return failureEnvelope("registry rotate", runtimeError(fmt.Errorf("new signing key has an invalid public part")))
	}
	newPublicKey := base64.StdEncoding.EncodeToString(newPublic)
	fingerprint, err := modkit.RegistryKeyFingerprint(newPublicKey)
	if err != nil {
		return failureEnvelope("registry rotate", runtimeError(err))
	}
	return Result{
		Envelope: normalizeEnvelope(modkit.Envelope{
			Command: "registry rotate", OK: true, Exit: exitOK,
			Generated: []string{modkit.RegistryKeyRotationPath, "registry.snapshot.old.sig", "registry.snapshot.new.sig"},
			Resolved:  []string{fingerprint},
		}),
		Payload: map[string]any{
			"new_fingerprint": fingerprint, "new_public_key": newPublicKey, "not_before": notBefore, "dir": dir,
		},
	}, nil
}

// runRegistrySet drives `ggg registry add|remove|update`: every one previews
// the exact registry source set and applies it through the same planned
// transaction as sync, so the lock, the graph, and the project file move
// together or not at all.
func runRegistrySet(ctx context.Context, cc CommandContext, action string, parsed parsedArgs) (Result, error) {
	project, err := cc.Controller.loadProject()
	if err != nil {
		return failureEnvelope("registry "+action, err)
	}
	current := append([]modkit.ProjectRegistry(nil), project.Registries...)
	var replacement []modkit.ProjectRegistry
	var payload map[string]any

	switch action {
	case "add":
		if len(parsed.positional) != 1 {
			return Result{}, usageError("ggg registry add github:OWNER/REPO|directory:PATH --namespace NAMESPACE")
		}
		ref := parsed.value("ref", "main")
		candidate, err := parseRegistrySource(parsed.positional[0], ref)
		if err != nil {
			return Result{}, usageError(err.Error())
		}
		candidate.Namespace = parsed.value("namespace", "")
		if candidate.Namespace == "" {
			if cc.AsJSON {
				return Result{}, usageError("registry add requires --namespace")
			}
			line, readErr := readLine(cc, "Registry namespace: ")
			if readErr != nil {
				return Result{}, readErr
			}
			candidate.Namespace = strings.TrimSpace(line)
			if candidate.Namespace == "" {
				return Result{}, usageError("registry add requires --namespace")
			}
		}
		if candidate.Source == "github" && candidate.PublicKey == "" {
			publicKey := parsed.value("public-key", "")
			if publicKey == "" {
				return Result{}, usageError("remote registries are signed: registry add requires --public-key")
			}
			candidate.PublicKey = publicKey
		}
		for _, registry := range current {
			if registry.Namespace == candidate.Namespace {
				return failureEnvelope("registry add", refusalError(fmt.Errorf(
					"namespace %q is already configured; remove it first", candidate.Namespace)))
			}
		}
		replacement = append(current, candidate)
		payload, err = cc.Controller.previewRegistryAdd(ctx, current, candidate)
		if err != nil {
			return failureEnvelope("registry add", err)
		}
	case "remove":
		if len(parsed.positional) != 1 {
			return Result{}, usageError("ggg registry remove NAMESPACE")
		}
		namespace := parsed.positional[0]
		found := false
		replacement = make([]modkit.ProjectRegistry, 0, len(current))
		for _, registry := range current {
			if registry.Namespace == namespace {
				found = true
				continue
			}
			replacement = append(replacement, registry)
		}
		if !found {
			return failureEnvelope("registry remove", refusalError(fmt.Errorf("registry %q is not configured in this project", namespace)))
		}
		if len(replacement) == 0 {
			return failureEnvelope("registry remove", refusalError(fmt.Errorf("refusing to remove the last registry source")))
		}
		payload = map[string]any{"removed": namespace}
	case "update":
		namespace := parsed.value("registry", "")
		ref := parsed.value("ref", "")
		if namespace != "" && ref == "" {
			return Result{}, usageError("registry update --registry requires --ref")
		}
		replacement = make([]modkit.ProjectRegistry, 0, len(current))
		moved := []string{}
		for _, registry := range current {
			if namespace == "" || registry.Namespace == namespace {
				if ref != "" {
					registry.Ref = ref
					moved = append(moved, registry.Namespace)
				}
			}
			replacement = append(replacement, registry)
		}
		if namespace != "" && len(moved) == 0 {
			return failureEnvelope("registry update", refusalError(fmt.Errorf("registry %q is not configured in this project", namespace)))
		}
		if len(current) > 1 && namespace == "" && ref != "" {
			return Result{}, usageError("registry update --ref requires exactly one configured registry; name the target with --registry")
		}
		payload = map[string]any{"moved": moved}
	default:
		return Result{}, usageError("unknown registry set action " + action)
	}

	mutation := RegistryMutation{SetRegistries: replacement}
	if payload == nil {
		payload = map[string]any{}
	}
	result, err := drivePlanMutation(ctx, cc, "registry "+action, mutation, false)
	if err != nil {
		return result, err
	}
	mergePayload(result, payload)
	return result, nil
}

// previewRegistryAdd resolves the union of the current sources and the
// candidate, so namespace collisions, duplicate scoped ids, and canonical
// prefix collisions refuse before anything is proposed, and reports exactly
// what the brief promises: namespace, fingerprint, canonical module, modules,
// dependencies, and the module diff the source brings.
func (c *Controller) previewRegistryAdd(ctx context.Context, current []modkit.ProjectRegistry, candidate modkit.ProjectRegistry) (map[string]any, error) {
	engine, err := c.engine(false)
	if err != nil {
		return nil, err
	}
	fingerprint := ""
	if candidate.PublicKey != "" {
		fingerprint, err = modkit.RegistryKeyFingerprint(candidate.PublicKey)
		if err != nil {
			return nil, refusalError(fmt.Errorf("public key: %w", err))
		}
	}
	candidateCatalog, _, err := engine.Catalog(ctx, []modkit.ProjectRegistry{candidate})
	if err != nil {
		return nil, refusalError(err)
	}
	if _, _, err := engine.Catalog(ctx, append(append([]modkit.ProjectRegistry{}, current...), candidate)); err != nil {
		return nil, refusalError(err)
	}
	lock := modkit.Lock{}
	if data, readErr := os.ReadFile(c.rootDir() + string(os.PathSeparator) + modkit.LockFileName); readErr == nil {
		parsed, parseErr := modkit.ParseLock(data)
		var stale modkit.EngineContractError
		if errors.As(parseErr, &stale) {
			return nil, refusalError(parseErr)
		}
		if parseErr == nil {
			lock = parsed
		}
	}
	installed := map[string]struct{}{}
	for _, module := range lock.Modules {
		if module.Reason != modkit.TombstoneReason {
			installed[module.ID] = struct{}{}
		}
	}
	modules := make([]string, 0, len(candidateCatalog.Modules))
	dependencies := map[string]string{}
	for _, module := range candidateCatalog.Modules {
		modules = append(modules, module.ID)
		for _, dependency := range module.Dependencies.Go {
			dependencies[dependency.Module] = dependency.Version
		}
	}
	sort.Strings(modules)
	diff := make([]string, 0)
	for _, id := range modules {
		if _, seen := installed[id]; !seen {
			diff = append(diff, id)
		}
	}
	deps := make([]string, 0, len(dependencies))
	for name, version := range dependencies {
		deps = append(deps, name+"@"+version)
	}
	sort.Strings(deps)
	return map[string]any{
		"namespace": candidate.Namespace, "fingerprint": fingerprint,
		"canonical_module": candidateCatalog.CanonicalModule, "modules": modules,
		"dependencies": deps, "new_modules": diff,
	}, nil
}

func parseRegistrySource(value, ref string) (modkit.ProjectRegistry, error) {
	kind, location, ok := strings.Cut(value, ":")
	if !ok || location == "" {
		return modkit.ProjectRegistry{}, fmt.Errorf("registry must be github:OWNER/REPO or directory:PATH")
	}
	switch kind {
	case "github":
		return modkit.ProjectRegistry{Source: "github", Repository: location, Ref: ref}, nil
	case "directory":
		return modkit.ProjectRegistry{Source: "directory", Path: location}, nil
	default:
		return modkit.ProjectRegistry{}, fmt.Errorf("registry must be github:OWNER/REPO or directory:PATH")
	}
}

// mergePayload folds preview facts into a rendered result's payload.
func mergePayload(result Result, payload map[string]any) {
	if result.Payload == nil {
		result.Payload = payload
		return
	}
	for key, value := range payload {
		result.Payload[key] = value
	}
}
