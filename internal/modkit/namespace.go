package modkit

import (
	"context"
	"fmt"
	"strings"
)

type namespaceOwners map[string]string

func (owners namespaceOwners) claim(namespace, value, module string) error {
	key := namespace + "\x00" + value
	if previous, exists := owners[key]; exists {
		return fmt.Errorf("%s namespace %q collision between %s and %s", namespace, value, previous, module)
	}
	owners[key] = module
	return nil
}

func preflightNamespaces(ctx context.Context, modules []Manifest) error {
	claims := make(namespaceOwners)
	targets := make(namespaceOwners)
	migrationIDs := make(namespaceOwners)
	migrationSources := make(namespaceOwners)
	routeIDs := make(namespaceOwners)
	routePatterns := make(namespaceOwners)
	jobs := make(namespaceOwners)
	contentIDs := make(namespaceOwners)
	contentPaths := make(namespaceOwners)
	navigation := make(namespaceOwners)
	slots := make(namespaceOwners)
	uiNames := make(namespaceOwners)
	assetIDs := make(namespaceOwners)
	assetPaths := make(namespaceOwners)
	environment := make(namespaceOwners)
	data := make(namespaceOwners)

	for _, module := range modules {
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, file := range module.Files {
			if file.Target == "gogogadget.json" || file.Target == "gogogadget.lock.json" || file.Target == "go.mod" {
				return fmt.Errorf("target namespace %q is reserved and cannot be owned by %s", file.Target, module.ID)
			}
			if err := targets.claim("target", file.Target, module.ID); err != nil {
				return err
			}
		}
		for _, migration := range module.Migrations {
			if err := migrationIDs.claim("migration id", migration.ID, module.ID); err != nil {
				return err
			}
			if err := migrationSources.claim("migration source", migration.Source, module.ID); err != nil {
				return err
			}
		}

		claimSets := []struct {
			name   string
			values []string
		}{
			{"package", module.Claims.Packages}, {"route", module.Claims.Routes},
			{"job", module.Claims.Jobs}, {"environment", module.Claims.Environment},
			{"i18n", module.Claims.I18n}, {"query", module.Claims.Queries},
			{"openapi", module.Claims.OpenAPI}, {"content type", module.Claims.ContentTypes},
			{"ui", module.Claims.UI}, {"asset", module.Claims.Assets}, {"data", module.Claims.Data},
		}
		for _, set := range claimSets {
			for _, value := range set.values {
				if err := claims.claim(set.name, value, module.ID); err != nil {
					return err
				}
			}
		}

		for _, route := range module.Runtime.Routes {
			if err := routeIDs.claim("route id", route.ID, module.ID); err != nil {
				return err
			}
			for _, method := range ownedRouteMethods(route.Method) {
				key := method + " " + normalizeRoutePattern(route.Pattern)
				if err := routePatterns.claim("route", key, module.ID); err != nil {
					return err
				}
			}
		}
		for _, job := range module.Runtime.Jobs {
			if err := jobs.claim("job kind", job.Kind, module.ID); err != nil {
				return err
			}
		}
		for _, content := range module.Runtime.ContentTypes {
			if err := contentIDs.claim("content id", content.ID, module.ID); err != nil {
				return err
			}
			for _, contentPath := range content.Paths {
				normalized := normalizeRoutePattern(contentPath)
				if err := contentPaths.claim("content path", normalized, module.ID); err != nil {
					return err
				}
				for _, method := range []string{"GET", "HEAD"} {
					if err := routePatterns.claim("route", method+" "+normalized, module.ID); err != nil {
						return err
					}
				}
				if content.Mode == ContentModePages {
					slugPattern := strings.TrimSuffix(normalized, "/") + "/{}"
					for _, method := range []string{"GET", "HEAD"} {
						if err := routePatterns.claim("route", method+" "+slugPattern, module.ID); err != nil {
							return err
						}
					}
				}
			}
		}
		for _, item := range module.Runtime.Navigation {
			if err := navigation.claim("navigation id", item.ID, module.ID); err != nil {
				return err
			}
		}
		for _, item := range module.Runtime.Slots {
			if err := slots.claim("slot id", item.ID, module.ID); err != nil {
				return err
			}
		}
		for _, item := range module.Runtime.UI {
			if err := uiNames.claim("ui name", item.Name, module.ID); err != nil {
				return err
			}
		}
		for _, item := range module.Runtime.Assets {
			if err := assetIDs.claim("asset id", item.ID, module.ID); err != nil {
				return err
			}
			if err := assetPaths.claim("asset path", item.Path, module.ID); err != nil {
				return err
			}
		}
		for _, item := range module.Environment {
			if err := environment.claim("environment key", item.Key, module.ID); err != nil {
				return err
			}
		}
		for _, item := range module.Data {
			identity := item.Table + "/" + item.RowDiscriminator
			if err := data.claim("data table/discriminator", identity, module.ID); err != nil {
				return err
			}
		}
	}
	if err := validateProviderInventory(modules); err != nil { return err }
	return nil
}

func validateProviderInventory(modules []Manifest) error {
	slots := map[string]ProviderSlotContribution{}
	for _, module := range modules {
		for _, slot := range module.Runtime.ProviderSlots {
			if _, exists := slots[slot.ID]; exists { return fmt.Errorf("provider slot %q declared by multiple modules", slot.ID) }
			slots[slot.ID] = slot
		}
	}
	for _, module := range modules {
		sys := module.Runtime.System
		if sys == nil || sys.Adapter == nil { continue }
		slot, ok := slots[sys.Adapter.Slot]
		if !ok { return fmt.Errorf("adapter %s references undeclared provider slot %q", module.ID, sys.Adapter.Slot) }
		want := map[string]string{}
		for _, capability := range slot.Capabilities { want[capability.Capability] = capability.Type }
		got := map[string]string{}
		for _, provide := range sys.Provides {
			if _, already := got[provide.Capability]; already { return fmt.Errorf("adapter %s provides capability %q more than once", module.ID, provide.Capability) }
			got[provide.Capability] = provide.Type
		}
		if len(want) != len(got) { return fmt.Errorf("adapter %s capability set does not exactly match provider slot %s", module.ID, sys.Adapter.Slot) }
		for capability, typ := range want {
			if got[capability] != typ { return fmt.Errorf("adapter %s capability %q type %q does not match slot type %q", module.ID, capability, got[capability], typ) }
		}
	}
	return nil
}

func ownedRouteMethods(method string) []string {
	switch method {
	case "GET":
		return []string{"GET", "HEAD"}
	case "HEAD":
		return []string{"HEAD"}
	default:
		return []string{method}
	}
}

func normalizeRoutePattern(pattern string) string {
	if pattern == "/" {
		return pattern
	}
	segments := strings.Split(pattern, "/")
	for i, segment := range segments {
		if len(segment) < 3 || segment[0] != '{' || segment[len(segment)-1] != '}' {
			continue
		}
		wildcard := segment[1 : len(segment)-1]
		switch {
		case wildcard == "$":
			segments[i] = "{$}"
		case strings.HasSuffix(wildcard, "..."):
			segments[i] = "{...}"
		default:
			segments[i] = "{}"
		}
	}
	return strings.Join(segments, "/")
}
