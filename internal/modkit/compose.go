package modkit

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// GenerateComposeFiles renders the development and test Compose projects from
// the selected provider targets. It is exported so genesis and focused tests
// use the exact generator path used by sync.
func GenerateComposeFiles(lock Lock, graph []Manifest) ([]GeneratedFile, error) {
	byID := make(map[string]Manifest, len(graph))
	for _, module := range graph {
		byID[module.ID] = module
	}
	files := make([]GeneratedFile, 0, 2)
	for _, environment := range []string{"development", "test"} {
		content, err := renderCompose(environment, lock, byID)
		if err != nil {
			return nil, err
		}
		name := "compose.yaml"
		if environment == "test" {
			name = "compose.test.yaml"
		}
		files = append(files, GeneratedFile{Path: name, Content: content})
	}
	return files, nil
}

func emitComposeRegistry(_ context.Context, _ string, lock Lock, graph []Manifest) ([]GeneratedFile, error) {
	return GenerateComposeFiles(lock, graph)
}

func renderCompose(environment string, lock Lock, modules map[string]Manifest) (string, error) {
	type selectedService struct {
		name    string
		adapter string
		target  ServiceTarget
		service LocalService
	}
	selected := make([]selectedService, 0)
	serviceNames := map[string]string{}
	hostPorts := map[int]string{}
	volumeNames := map[string]string{}

	slots := make([]string, 0, len(lock.Providers))
	for slot := range lock.Providers {
		slots = append(slots, slot)
	}
	sort.Strings(slots)
	for _, slot := range slots {
		choice := providerSelectionForEnvironment(lock.Providers[slot], environment)
		if choice.Adapter == "" {
			continue
		}
		module, ok := modules[choice.Adapter]
		if !ok || module.Runtime.System == nil || module.Runtime.System.Adapter == nil {
			return "", fmt.Errorf("compose %s: selected adapter %s for %s is not installed", environment, choice.Adapter, slot)
		}
		var target *ServiceTarget
		for i := range module.Runtime.System.Adapter.Targets {
			if module.Runtime.System.Adapter.Targets[i].ID == choice.Target {
				target = &module.Runtime.System.Adapter.Targets[i]
				break
			}
		}
		if target == nil {
			return "", fmt.Errorf("compose %s: selected target %s@%s is not declared", environment, choice.Adapter, choice.Target)
		}
		if target.LocalService == nil {
			continue
		}
		if target.Mode != "development" && target.Mode != "self-hosted" {
			return "", fmt.Errorf("compose %s: target %s@%s has a local service but mode %s", environment, choice.Adapter, choice.Target, target.Mode)
		}
		if !strings.Contains(target.LocalService.Container, "@sha256:") {
			return "", fmt.Errorf("compose %s: target %s@%s image must be digest-pinned", environment, choice.Adapter, choice.Target)
		}
		name := composeScopedName(choice.Adapter, choice.Target)
		if owner, exists := serviceNames[name]; exists {
			return "", fmt.Errorf("compose %s: service name %q collides between %s and %s", environment, name, owner, choice.Adapter)
		}
		serviceNames[name] = choice.Adapter
		for _, port := range target.LocalService.Ports {
			if owner, exists := hostPorts[port.DefaultHost]; exists {
				return "", fmt.Errorf("compose %s: host port %d collides between %s and %s", environment, port.DefaultHost, owner, name)
			}
			hostPorts[port.DefaultHost] = name
		}
		for _, volume := range target.LocalService.Volumes {
			volumeName := composeScopedName(choice.Adapter, choice.Target, volume.Name)
			if owner, exists := volumeNames[volumeName]; exists {
				return "", fmt.Errorf("compose %s: volume name %q collides between %s and %s", environment, volumeName, owner, name)
			}
			volumeNames[volumeName] = name
		}
		selected = append(selected, selectedService{name: name, adapter: choice.Adapter, target: *target, service: *target.LocalService})
	}

	root := &yaml.Node{Kind: yaml.MappingNode}
	services := &yaml.Node{Kind: yaml.MappingNode}
	appendYAMLMap(root, "services", services)
	app := &yaml.Node{Kind: yaml.MappingNode}
	appendYAMLMap(app, "build", yamlScalar("."))
	appendYAMLMap(app, "env_file", yamlSequence(".ggg/env/"+environment+".env"))
	if len(selected) > 0 {
		depends := &yaml.Node{Kind: yaml.MappingNode}
		for _, item := range selected {
			condition := &yaml.Node{Kind: yaml.MappingNode}
			appendYAMLMap(condition, "condition", yamlScalar("service_healthy"))
			appendYAMLMap(depends, item.name, condition)
		}
		appendYAMLMap(app, "depends_on", depends)
	}
	appendYAMLMap(services, "app", app)

	volumes := &yaml.Node{Kind: yaml.MappingNode}
	for _, item := range selected {
		node := &yaml.Node{Kind: yaml.MappingNode}
		appendYAMLMap(node, "image", yamlScalar(item.service.Container))
		if len(item.service.Ports) > 0 {
			ports := &yaml.Node{Kind: yaml.SequenceNode}
			for _, port := range item.service.Ports {
				ports.Content = append(ports.Content, yamlScalar(strconv.Itoa(port.DefaultHost)+":"+strconv.Itoa(port.Container)))
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
		command := "nc -z 127.0.0.1 " + strconv.Itoa(item.service.Health.Port)
		if item.service.Health.Kind == "http" {
			command = "wget -qO- http://127.0.0.1:" + strconv.Itoa(item.service.Health.Port) + item.service.Health.Path
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
