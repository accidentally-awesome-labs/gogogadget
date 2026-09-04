package modkit

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// GenerateComposeFiles renders the development and test Compose projects from
// the selected provider targets. It is exported so genesis and focused tests
// use the exact generator path used by sync.
//
// Both files are planned before either is rendered. Each file is its own
// Compose project, so nothing inside one file can observe the other's
// published ports — and a host has exactly one port space, which is where the
// collision that actually bit lives. Planning the set first is what lets the
// generator keep the promise that host-port collisions refuse generation.
func GenerateComposeFiles(lock Lock, graph []Manifest) ([]GeneratedFile, error) {
	byID := make(map[string]Manifest, len(graph))
	for _, module := range graph {
		byID[module.ID] = module
	}
	plans := make([]composePlan, 0, len(composeEnvironments))
	for _, environment := range composeEnvironments {
		plan, err := planCompose(environment, lock, byID)
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	if err := refuseCrossEnvironmentPorts(plans); err != nil {
		return nil, err
	}
	if err := refuseUndeclaredPortOverrides(lock.Ports, plans); err != nil {
		return nil, err
	}
	files := make([]GeneratedFile, 0, len(plans))
	for _, plan := range plans {
		content, err := renderCompose(plan)
		if err != nil {
			return nil, err
		}
		files = append(files, GeneratedFile{Path: ComposeFileName(plan.environment), Content: content})
	}
	return files, nil
}

// composeEnvironments are the environments that have a generated Compose file.
// Production deploys through a deploy target and has none.
var composeEnvironments = []string{"development", "test"}

// ComposeFileName is the generated Compose file one environment stands up.
func ComposeFileName(environment string) string {
	if environment == composeTestEnvironment {
		return "compose.test.yaml"
	}
	return "compose.yaml"
}

func emitComposeRegistry(_ context.Context, _ string, lock Lock, graph []Manifest) ([]GeneratedFile, error) {
	return GenerateComposeFiles(lock, graph)
}

type selectedService struct {
	name    string
	slot    string
	adapter string
	target  ServiceTarget
	service LocalService
	// published is every declared port with the host port this environment
	// resolved for it.
	published []publishedPort
}

// publishedPort is one declared container port and the host port it is
// published on in one environment.
type publishedPort struct {
	host      int
	container int
}

// composePlan is everything one generated Compose file publishes, resolved
// before any file is written.
type composePlan struct {
	environment string
	// appHost is the host port the app service publishes on, or zero when it
	// publishes none.
	appHost  int
	selected []selectedService
	// owners maps each published host port to the `<service>/<port>` key that
	// publishes it, for the collision refusals. The key, not the service, is
	// the useful owner: two ports of ONE service can land on one host port
	// (nothing refuses a duplicate default_host within a declaration), and
	// naming the service twice would report "collides between X and X" without
	// saying which two ports to move. It is also exactly the string an
	// operator writes under `ports` to fix it.
	owners map[int]string
	// declared is every override key this environment can honour, for the
	// override refusal and the list it prints.
	declared map[string]struct{}
}

func planCompose(environment string, lock Lock, modules map[string]Manifest) (composePlan, error) {
	plan := composePlan{
		environment: environment,
		selected:    make([]selectedService, 0),
		owners:      map[int]string{},
		declared:    map[string]struct{}{},
	}

	// The app service is resolved first, so an adapter that lands on its port
	// names `app/http` as the other owner rather than the reverse.
	plan.declared[appPortKey] = struct{}{}
	appHost, overridden := lock.Ports[appPortKey].ForEnvironment(environment)
	if !overridden && environment != composeTestEnvironment {
		appHost = appPort
	}
	if appHost != 0 {
		plan.appHost = appHost
		plan.owners[appHost] = appPortKey
	}

	serviceNames := map[string]string{}
	volumeNames := map[string]string{}
	for _, slot := range sortedKeys(lock.Providers) {
		choice := providerSelectionForEnvironment(lock.Providers[slot], environment)
		if choice.Adapter == "" {
			continue
		}
		module, ok := modules[choice.Adapter]
		if !ok || module.Runtime.System == nil || module.Runtime.System.Adapter == nil {
			return composePlan{}, fmt.Errorf("compose %s: selected adapter %s for %s is not installed", environment, choice.Adapter, slot)
		}
		var target *ServiceTarget
		for i := range module.Runtime.System.Adapter.Targets {
			if module.Runtime.System.Adapter.Targets[i].ID == choice.Target {
				target = &module.Runtime.System.Adapter.Targets[i]
				break
			}
		}
		if target == nil {
			return composePlan{}, fmt.Errorf("compose %s: selected target %s@%s is not declared", environment, choice.Adapter, choice.Target)
		}
		if target.LocalService == nil {
			continue
		}
		if target.Mode != "development" && target.Mode != "self-hosted" {
			return composePlan{}, fmt.Errorf("compose %s: target %s@%s has a local service but mode %s", environment, choice.Adapter, choice.Target, target.Mode)
		}
		if !strings.Contains(target.LocalService.Container, "@sha256:") {
			return composePlan{}, fmt.Errorf("compose %s: target %s@%s image must be digest-pinned", environment, choice.Adapter, choice.Target)
		}
		name := composeScopedName(choice.Adapter, choice.Target)
		if owner, exists := serviceNames[name]; exists {
			return composePlan{}, fmt.Errorf("compose %s: service name %q collides between %s and %s", environment, name, owner, choice.Adapter)
		}
		serviceNames[name] = choice.Adapter
		identity := choice.Adapter + "@" + choice.Target
		item := selectedService{name: name, slot: slot, adapter: choice.Adapter, target: *target, service: *target.LocalService}
		for _, port := range target.LocalService.Ports {
			key := identity + "/" + port.Name
			plan.declared[key] = struct{}{}
			host, err := effectiveHostPort(lock.Ports, environment, key, port.DefaultHost)
			if err != nil {
				return composePlan{}, err
			}
			if owner, exists := plan.owners[host]; exists {
				return composePlan{}, fmt.Errorf("compose %s: host port %d collides between %s and %s", environment, host, owner, key)
			}
			plan.owners[host] = key
			item.published = append(item.published, publishedPort{host: host, container: port.Container})
		}
		for _, volume := range target.LocalService.Volumes {
			volumeName := composeScopedName(choice.Adapter, choice.Target, volume.Name)
			if owner, exists := volumeNames[volumeName]; exists {
				return composePlan{}, fmt.Errorf("compose %s: volume name %q collides between %s and %s", environment, volumeName, owner, name)
			}
			volumeNames[volumeName] = name
		}
		plan.selected = append(plan.selected, item)
	}
	return plan, nil
}

// effectiveHostPort resolves the host port one declared Compose port is
// published on in one environment.
//
// Development publishes the port its owning target declares: that is the
// documented address every doc and configuration default names. Test publishes
// the same port shifted by testPortOffset, so the two stacks coexist on one
// host by construction and the shifted address stays readable — 5432 becomes
// 15432. Deriving it keeps a second hand-maintained set of numbers, and the
// drift that comes with it, out of the manifests: a target declares one port
// and both stacks follow from it.
func effectiveHostPort(overrides map[string]PortOverrides, environment, key string, declared int) (int, error) {
	if host, ok := overrides[key].ForEnvironment(environment); ok {
		return host, nil
	}
	if environment != composeTestEnvironment {
		return declared, nil
	}
	host := declared + testPortOffset
	if host > maxHostPort {
		return 0, fmt.Errorf(
			"compose test: declared host port %d for %s shifts to %d, past the %d ceiling; declare a lower default_host, or set a test port for %q under \"ports\" in %s",
			declared, key, host, maxHostPort, key, ProjectFileName)
	}
	return host, nil
}

// refuseCrossEnvironmentPorts refuses when two environments publish the same
// host port. Each file is generated on its own, so this is the only place the
// whole generated set is visible at once, and a project that cannot run its own
// test stack beside its development stack is the failure it exists to catch.
func refuseCrossEnvironmentPorts(plans []composePlan) error {
	for i, left := range plans {
		for _, right := range plans[i+1:] {
			for _, host := range sortedInts(left.owners) {
				owner, exists := right.owners[host]
				if !exists {
					continue
				}
				return fmt.Errorf(
					"compose: host port %d collides between %s (%s) and %s (%s); both stacks must be able to run at once, so move one with a \"ports\" override in %s",
					host, left.environment, left.owners[host], right.environment, owner, ProjectFileName)
			}
		}
	}
	return nil
}

// refuseUndeclaredPortOverrides refuses an override that names no port the
// environment's stack declares. Ignoring it would leave the operator with a
// committed decision the generator silently dropped — a service still on the
// port they moved it off.
func refuseUndeclaredPortOverrides(overrides map[string]PortOverrides, plans []composePlan) error {
	for _, key := range sortedKeys(overrides) {
		for _, plan := range plans {
			if _, ok := overrides[key].ForEnvironment(plan.environment); !ok {
				continue
			}
			if _, declared := plan.declared[key]; declared {
				continue
			}
			return fmt.Errorf("compose: port override %q names no port the %s stack declares; it declares %s",
				key, plan.environment, strings.Join(sortedKeys(plan.declared), ", "))
		}
	}
	return nil
}

func renderCompose(plan composePlan) (string, error) {
	// A distinct compose project per environment keeps container, network, and
	// volume names from the development and test stacks from colliding, even
	// though both files declare the same service roles.
	root := &yaml.Node{Kind: yaml.MappingNode}
	appendYAMLMap(root, "name", yamlScalar("gogogadget-"+plan.environment))
	services := &yaml.Node{Kind: yaml.MappingNode}
	appendYAMLMap(root, "services", services)
	app := &yaml.Node{Kind: yaml.MappingNode}
	appendYAMLMap(app, "build", yamlScalar("."))
	appendYAMLMap(app, "env_file", yamlSequence(".ggg/env/"+plan.environment+".env"))
	if plan.appHost != 0 {
		appendYAMLMap(app, "ports", yamlSequence(strconv.Itoa(plan.appHost)+":"+strconv.Itoa(appPort)))
	}
	appEnv := &yaml.Node{Kind: yaml.MappingNode}
	appendYAMLMap(appEnv, "APP_ENV", yamlScalar(plan.environment))
	appendYAMLMap(appEnv, "APP_URL", yamlScalar(appURL(plan)))
	if url := databaseURL(plan.selected); url != "" {
		appendYAMLMap(appEnv, "DATABASE_URL", yamlScalar(url))
	}
	appendYAMLMap(app, "environment", appEnv)
	if len(plan.selected) > 0 {
		depends := &yaml.Node{Kind: yaml.MappingNode}
		for _, item := range plan.selected {
			condition := &yaml.Node{Kind: yaml.MappingNode}
			appendYAMLMap(condition, "condition", yamlScalar("service_healthy"))
			appendYAMLMap(depends, item.name, condition)
		}
		appendYAMLMap(app, "depends_on", depends)
	}
	appendYAMLMap(services, composeAppService, app)

	volumes := &yaml.Node{Kind: yaml.MappingNode}
	for _, item := range plan.selected {
		node := &yaml.Node{Kind: yaml.MappingNode}
		appendYAMLMap(node, "image", yamlScalar(item.service.Container))
		if len(item.published) > 0 {
			ports := &yaml.Node{Kind: yaml.SequenceNode}
			for _, port := range item.published {
				ports.Content = append(ports.Content, yamlScalar(strconv.Itoa(port.host)+":"+strconv.Itoa(port.container)))
			}
			appendYAMLMap(node, "ports", ports)
		}
		if len(item.service.Environment) > 0 {
			env := &yaml.Node{Kind: yaml.MappingNode}
			for _, variable := range item.service.Environment {
				value := variable.Value
				if variable.FromKey != "" {
					value = "${" + variable.FromKey + "}"
				}
				appendYAMLMap(env, variable.Key, yamlScalar(value))
			}
			appendYAMLMap(node, "environment", env)
		}
		if len(item.service.Volumes) > 0 {
			mounts := &yaml.Node{Kind: yaml.SequenceNode}
			for _, volume := range item.service.Volumes {
				name := composeScopedName(item.adapter, item.target.ID, volume.Name)
				mounts.Content = append(mounts.Content, yamlScalar(name+":"+volume.Target))
				appendYAMLMap(volumes, name, &yaml.Node{Kind: yaml.MappingNode})
			}
			appendYAMLMap(node, "volumes", mounts)
		}
		health := &yaml.Node{Kind: yaml.MappingNode}
		command := ""
		if item.service.Health.Command != "" {
			command = item.service.Health.Command
		} else if item.service.Health.Kind == "http" {
			command = "wget -qO- http://127.0.0.1:" + strconv.Itoa(item.service.Health.Port) + item.service.Health.Path
		} else {
			command = "nc -z 127.0.0.1 " + strconv.Itoa(item.service.Health.Port)
		}
		appendYAMLMap(health, "test", yamlSequence("CMD-SHELL", command))
		appendYAMLMap(health, "interval", yamlScalar("2s"))
		appendYAMLMap(health, "timeout", yamlScalar("2s"))
		appendYAMLMap(health, "retries", yamlScalar("30"))
		appendYAMLMap(node, "healthcheck", health)
		appendYAMLMap(services, item.name, node)
	}
	if len(volumes.Content) > 0 {
		appendYAMLMap(root, "volumes", volumes)
	}
	data, err := yaml.Marshal(root)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// appURL is the origin the app service reports as its own. It follows the
// effective published port, never the declared default: a link built from a
// port nothing is listening on is a broken redirect, and the whole point of an
// override is that the port moved.
//
// An unpublished app has no host origin at all, so it reports the in-network
// one — the same derivation DATABASE_URL uses, and honest about the only place
// it can be reached from.
func appURL(plan composePlan) string {
	if plan.appHost == 0 {
		return "http://" + composeAppService + ":" + strconv.Itoa(appPort)
	}
	return "http://localhost:" + strconv.Itoa(plan.appHost)
}

// appPort is the container port the generated app service listens on, and the
// host port the development stack publishes it on.
const appPort = 8080

// composeAppService is the generated app service's name in every environment.
const composeAppService = "app"

// appPortKey is the `ports` override key for the app service's HTTP port.
const appPortKey = composeAppService + "/http"

// composeTestEnvironment is the one environment whose published ports are
// derived rather than declared.
const composeTestEnvironment = "test"

// testPortOffset shifts every host port the test stack publishes off the
// development stack's. It is deliberately a round 10000: the shifted port
// stays recognisable (5432 → 15432, 1025 → 11025), which matters when an
// operator reads it out of `docker ps` and points a tool at it.
const testPortOffset = 10000

// maxHostPort is the last valid TCP port.
const maxHostPort = 65535

// databaseSlot is the provider slot whose selected adapter supplies the
// project's Postgres. It is the one slot a connection string is derived from.
const databaseSlot = "ggg/database"

// selectedDatabaseService is the local Postgres service one environment
// selected, when the selected target declares one at all. A managed target
// (Neon) declares none, and then nothing local can be derived.
func selectedDatabaseService(selected []selectedService) (selectedService, bool) {
	for _, item := range selected {
		if item.slot == databaseSlot {
			return item, true
		}
	}
	return selectedService{}, false
}

// postgresDSN renders one connection string from a local service's declared
// credentials and one address, and reports whether it could be derived at all.
//
// The two derivations — the in-network one the compose app reads and the host
// one every host-side consumer reads — differ in nothing but that address, so
// they share this. Credentials come from the service's own declaration rather
// than from a literal, which is what keeps a changed POSTGRES_PASSWORD from
// splitting the two apart.
//
// A LocalServiceEnv legally sets exactly one of value or from_key, and a
// from_key names a value that exists only in the environment Compose expands
// against — nothing here can read it. Substituting the zero value would derive
// postgres://postgres:@host:port/… : silently wrong, written into a committed
// generated artifact, and trusted enough for `ggg db migrate` to mutate
// through. So a referenced credential derives NOTHING, and every consumer
// falls through to explicit configuration.
func postgresDSN(service LocalService, host string, port int) (string, bool) {
	user, password, name := "postgres", "postgres", "gogogadget"
	for _, variable := range service.Environment {
		var target *string
		switch variable.Key {
		case "POSTGRES_USER":
			target = &user
		case "POSTGRES_PASSWORD":
			target = &password
		case "POSTGRES_DB":
			target = &name
		default:
			continue
		}
		if variable.FromKey != "" {
			return "", false
		}
		*target = variable.Value
	}
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable", user, password, host, port, name), true
}

// databaseURL derives the in-network DATABASE_URL from the selected database
// adapter's local service declaration, so the compose app reaches the same
// Postgres the environment selected without hardcoded credentials.
//
// It addresses the service by name and container port, so it is unaffected by
// which host port that service is published on — including none. An empty
// result means nothing could be derived; postgresDSN says when.
func databaseURL(selected []selectedService) string {
	item, ok := selectedDatabaseService(selected)
	if !ok {
		return ""
	}
	port := 5432
	if len(item.service.Ports) > 0 {
		port = item.service.Ports[0].Container
	}
	url, _ := postgresDSN(item.service, item.name, port)
	return url
}

// hostDatabaseURL is databaseURL's sibling for a process running on the host
// rather than inside the Compose network: goose under `ggg db`, `cmd/seed`,
// `internal/db/testdb`, the Playwright harness, and the visual server. It
// addresses the same service through the host port this environment publishes
// it on, which is the number effectiveHostPort resolved — so the test stack
// derives 15432 without a second set of hand-maintained numbers.
//
// A service published on no host port yields nothing rather than a guess: the
// address would not exist. So does a service whose credentials are declared by
// reference — see postgresDSN.
func hostDatabaseURL(selected []selectedService) string {
	item, ok := selectedDatabaseService(selected)
	if !ok || len(item.published) == 0 {
		return ""
	}
	url, _ := postgresDSN(item.service, "localhost", item.published[0].host)
	return url
}

// DerivedEnvironmentValues is what this project's own provider selection and
// published host ports resolve to for one environment. It is THE derivation:
// `ggg db`, the generated configuration parser, `internal/db/testdb` and the
// Playwright harness all read it, so no consumer keeps a default of its own.
//
// A derived value differs from a manifest's declared default in the way that
// matters to anything destructive: the default is a documented guess at a live
// local address, while this reflects the adapter THIS project selected and the
// port THIS project publishes. That is why mutating commands refuse the former
// and accept the latter.
//
// Production derives nothing. It has no generated Compose file and therefore
// no local service, so there is no host address to name — a production
// connection string comes from the deployment environment or not at all.
func DerivedEnvironmentValues(lock Lock, graph []Manifest, environment string) (map[string]string, error) {
	values := map[string]string{}
	if !slices.Contains(composeEnvironments, environment) {
		return values, nil
	}
	byID := make(map[string]Manifest, len(graph))
	for _, module := range graph {
		byID[module.ID] = module
	}
	plan, err := planCompose(environment, lock, byID)
	if err != nil {
		return nil, err
	}
	if dsn := hostDatabaseURL(plan.selected); dsn != "" {
		values["DATABASE_URL"] = dsn
	}
	return values, nil
}

// sortedInts orders the keys of a port-indexed map, so every refusal reports
// the same collision for the same inputs.
func sortedInts[V any](m map[int]V) []int {
	out := make([]int, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Ints(out)
	return out
}

func providerSelectionForEnvironment(selections ProviderSelections, environment string) ProviderSelection {
	switch environment {
	case "development":
		return selections.Development
	case "test":
		return selections.Test
	case "production":
		return selections.Production
	default:
		return ProviderSelection{}
	}
}

// ComposeServiceName renders the compose service name for one adapter
// target — the same derivation the generator writes into compose.yaml, so
// commands that address a local service (database backup, restore, logs)
// name it identically.
func ComposeServiceName(adapter, target string) string {
	return composeScopedName(adapter, target)
}

func composeScopedName(parts ...string) string {
	joined := strings.Join(parts, "-")
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(joined) {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if ok {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func appendYAMLMap(node *yaml.Node, key string, value *yaml.Node) {
	node.Content = append(node.Content, yamlScalar(key), value)
}

func yamlScalar(value string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Value: value} }

func yamlSequence(values ...string) *yaml.Node {
	node := &yaml.Node{Kind: yaml.SequenceNode}
	for _, value := range values {
		node.Content = append(node.Content, yamlScalar(value))
	}
	return node
}
