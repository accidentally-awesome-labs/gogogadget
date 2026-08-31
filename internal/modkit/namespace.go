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
			{"provider slot", module.Claims.ProviderSlots}, {"provisioner", module.Claims.Provisioners},
			{"database op", module.Claims.DatabaseOps}, {"cli", module.Claims.CLI}, {"deploy", module.Claims.Deploy},
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
		if err := requireClaims(module); err != nil {
			return err
		}
	}
	if err := validateProviderInventory(modules); err != nil {
		return err
	}
	return nil
}

func validateProviderInventory(modules []Manifest) error {
	slots := map[string]ProviderSlotContribution{}
	for _, module := range modules {
		for _, slot := range module.Runtime.ProviderSlots {
			if _, exists := slots[slot.ID]; exists {
				return fmt.Errorf("provider slot %q declared by multiple modules", slot.ID)
			}
			slots[slot.ID] = slot
		}
	}
	type provider struct {
		module, slot, typ string
		adapter           bool
	}
	providers := map[string]provider{}
	for _, module := range modules {
		sys := module.Runtime.System
		if sys == nil {
			continue
		}
		for _, provide := range sys.Provides {
			current := provider{module: module.ID, typ: provide.Type, adapter: sys.Adapter != nil}
			if sys.Adapter != nil {
				current.slot = sys.Adapter.Slot
			}
			if previous, exists := providers[provide.Capability]; exists {
				if !previous.adapter || !current.adapter {
					return fmt.Errorf("runtime capability %q has multiple non-adapter providers (%s and %s)", provide.Capability, previous.module, current.module)
				}
				if previous.slot != current.slot || previous.typ != current.typ {
					return fmt.Errorf("runtime capability %q conflicts across provider slots or types (%s and %s)", provide.Capability, previous.module, current.module)
				}
				continue
			}
			providers[provide.Capability] = current
		}
	}
	for _, module := range modules {
		sys := module.Runtime.System
		if sys == nil || sys.Adapter == nil {
			continue
		}
		slot, ok := slots[sys.Adapter.Slot]
		if !ok {
			return fmt.Errorf("adapter %s references undeclared provider slot %q", module.ID, sys.Adapter.Slot)
		}
		want := map[string]string{}
		for _, capability := range slot.Capabilities {
			want[capability.Capability] = capability.Type
		}
		got := map[string]string{}
		for _, provide := range sys.Provides {
			if _, already := got[provide.Capability]; already {
				return fmt.Errorf("adapter %s provides capability %q more than once", module.ID, provide.Capability)
			}
			got[provide.Capability] = provide.Type
		}
		if len(want) != len(got) {
			return fmt.Errorf("adapter %s capability set does not exactly match provider slot %s", module.ID, sys.Adapter.Slot)
		}
		for _, capability := range sortedKeys(want) {
			typ := want[capability]
			if got[capability] != typ {
				return fmt.Errorf("adapter %s capability %q type %q does not match slot type %q", module.ID, capability, got[capability], typ)
			}
		}
		for _, target := range sys.Adapter.Targets {
			if (target.Automation == "provision" || target.Automation == "configure") && !hasContribution(module.Runtime.Provisioners, target.Provisioner) {
				return fmt.Errorf("adapter %s target %s references unclaimed provisioner %q", module.ID, target.ID, target.Provisioner)
			}
			if target.DatabaseOperator != "" {
				if sys.Adapter.Slot != "ggg/database" {
					return fmt.Errorf("adapter %s target %s has database operator outside ggg/database", module.ID, target.ID)
				}
				if !hasDatabaseContribution(module.Runtime.DatabaseOps, target.DatabaseOperator) {
					return fmt.Errorf("adapter %s target %s references unclaimed database operator %q", module.ID, target.ID, target.DatabaseOperator)
				}
			}
		}
	}
	return nil
}

func hasContribution(items []ProvisionerContribution, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}
func hasDatabaseContribution(items []DatabaseOpsContribution, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}
func requireClaims(module Manifest) error {
	check := func(kind string, declared, claims []string) error {
		declaredSet := map[string]struct{}{}
		for _, id := range declared {
			declaredSet[id] = struct{}{}
		}
		claimSet := map[string]struct{}{}
		for _, id := range claims {
			claimSet[id] = struct{}{}
			if _, ok := declaredSet[id]; !ok {
				return fmt.Errorf("%s %s has no matching declaration", kind, id)
			}
		}
		for _, id := range declared {
			if _, ok := claimSet[id]; !ok {
				return fmt.Errorf("%s %s is not declared in matching namespace claim", kind, id)
			}
		}
		return nil
	}
	providerSlots := make([]string, 0, len(module.Runtime.ProviderSlots))
	for _, value := range module.Runtime.ProviderSlots {
		providerSlots = append(providerSlots, value.ID)
	}
	if err := check("provider slot", providerSlots, module.Claims.ProviderSlots); err != nil {
		return err
	}
	provisioners := make([]string, 0, len(module.Runtime.Provisioners))
	for _, value := range module.Runtime.Provisioners {
		provisioners = append(provisioners, value.ID)
	}
	if err := check("provisioner", provisioners, module.Claims.Provisioners); err != nil {
		return err
	}
	databaseOps := make([]string, 0, len(module.Runtime.DatabaseOps))
	for _, value := range module.Runtime.DatabaseOps {
		databaseOps = append(databaseOps, value.ID)
	}
	if err := check("database op", databaseOps, module.Claims.DatabaseOps); err != nil {
		return err
	}
	deploy := make([]string, 0, len(module.Runtime.Deploy))
	for _, value := range module.Runtime.Deploy {
		deploy = append(deploy, value.ID)
	}
	if err := check("deploy", deploy, module.Claims.Deploy); err != nil {
		return err
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
