package devserver

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// composePortBinding mirrors one entry from a service's `ports:` list,
// covering both short ("host:container[/proto]") and long ({target, published,
// ...}) forms. Only the fields the walk logic needs are tracked — protocol
// and host_ip are preserved on round-trip but otherwise opaque.
type composePortBinding struct {
	HostIP    string // optional: empty / "127.0.0.1" / "0.0.0.0"
	HostPort  int    // 0 = unspecified (random); we don't walk these
	Container int    // container-internal port (always set)
	Protocol  string // "tcp" by default; emitted when non-empty
}

// composeService is the subset of one compose service definition we read
// for port walking — only the name and ports list. Everything else in the
// service is left to the original compose file.
type composeService struct {
	Name  string
	Ports []composePortBinding
}

// parseComposePorts loads path, reads the top-level `services:` map, and
// returns each service's port bindings in declaration order. Services
// without any ports are still returned (with an empty Ports list) so the
// caller can iterate uniformly. YAML parse errors and unknown port forms
// surface as errors — silent fallback would mask a typo and let a
// collision sneak through.
func parseComposePorts(path string) ([]composeService, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read compose file: %w", err)
	}
	var doc struct {
		Services map[string]yaml.Node `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse compose file %s: %w", path, err)
	}

	// yaml.Node preserves declaration order via the parent MappingNode,
	// but Unmarshal into map[string]Node loses that. Re-parse the top
	// level to a Node so we can walk in source order — important for
	// readable override emission.
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse compose file %s: %w", path, err)
	}
	servicesNode := findChildMappingNode(&root, "services")
	if servicesNode == nil {
		return nil, nil
	}

	var out []composeService
	for i := 0; i+1 < len(servicesNode.Content); i += 2 {
		nameNode := servicesNode.Content[i]
		valNode := servicesNode.Content[i+1]
		if nameNode.Kind != yaml.ScalarNode || valNode.Kind != yaml.MappingNode {
			continue
		}
		svc := composeService{Name: nameNode.Value}
		portsNode := findChildNode(valNode, "ports")
		if portsNode != nil && portsNode.Kind == yaml.SequenceNode {
			for _, item := range portsNode.Content {
				binding, err := parseComposePortNode(item)
				if err != nil {
					return nil, fmt.Errorf("service %q: %w", svc.Name, err)
				}
				svc.Ports = append(svc.Ports, binding)
			}
		}
		out = append(out, svc)
	}
	return out, nil
}

// findChildMappingNode locates the value node for key in node (treated as a
// document or mapping). Returns nil when key is absent or the value isn't a
// mapping. Walks one level into a DocumentNode wrapper.
func findChildMappingNode(node *yaml.Node, key string) *yaml.Node {
	n := findChildNode(node, key)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	return n
}

// findChildNode returns the value node for key in a YAML mapping. Handles
// the DocumentNode wrapper that the top-level Unmarshal produces.
func findChildNode(node *yaml.Node, key string) *yaml.Node {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.DocumentNode {
		for _, c := range node.Content {
			if found := findChildNode(c, key); found != nil {
				return found
			}
		}
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		k := node.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// parseComposePortNode resolves one entry of a `ports:` list, accepting
// both short-form scalars ("5432:5432") and long-form maps ({target, ...}).
func parseComposePortNode(n *yaml.Node) (composePortBinding, error) {
	switch n.Kind {
	case yaml.ScalarNode:
		return parseComposeShortPort(n.Value)
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i].Value
			var v any
			if err := n.Content[i+1].Decode(&v); err != nil {
				return composePortBinding{}, fmt.Errorf("invalid value for %q: %w", k, err)
			}
			m[k] = v
		}
		return parseComposeLongPort(m)
	default:
		return composePortBinding{}, fmt.Errorf("unsupported port node kind %v", n.Kind)
	}
}

// parseComposeShortPort handles the short form: "[host_ip:][host:]container[/proto]".
func parseComposeShortPort(s string) (composePortBinding, error) {
	var b composePortBinding
	if i := strings.LastIndex(s, "/"); i > 0 {
		b.Protocol = s[i+1:]
		s = s[:i]
	}
	lastColon := strings.LastIndex(s, ":")
	if lastColon < 0 {
		// Container port only (host random).
		cp, err := strconv.Atoi(s)
		if err != nil {
			return b, fmt.Errorf("invalid container port %q", s)
		}
		b.Container = cp
		return b, nil
	}

	cp, err := strconv.Atoi(s[lastColon+1:])
	if err != nil {
		return b, fmt.Errorf("invalid container port %q", s[lastColon+1:])
	}
	b.Container = cp

	hostSpec := s[:lastColon]
	if hp, err := strconv.Atoi(hostSpec); err == nil {
		b.HostPort = hp
		return b, nil
	}

	host, portStr, err := net.SplitHostPort(hostSpec)
	if err != nil {
		return b, fmt.Errorf("invalid short-form port %q", s)
	}
	hp, err := strconv.Atoi(portStr)
	if err != nil {
		return b, fmt.Errorf("invalid host port %q", portStr)
	}
	b.HostIP = host
	b.HostPort = hp
	return b, nil
}

// parseComposeLongPort handles the long form mapping. `published` may be
// either an int or a numeric string (compose accepts both).
func parseComposeLongPort(m map[string]any) (composePortBinding, error) {
	var b composePortBinding
	target, err := readPortNumber(m["target"])
	if err != nil {
		return b, fmt.Errorf("target: %w", err)
	}
	b.Container = target
	if v, ok := m["published"]; ok {
		published, err := readPortNumber(v)
		if err != nil {
			return b, fmt.Errorf("published: %w", err)
		}
		b.HostPort = published
	}
	if v, ok := m["host_ip"].(string); ok {
		b.HostIP = v
	}
	if v, ok := m["protocol"].(string); ok {
		b.Protocol = v
	}
	return b, nil
}

func readPortNumber(v any) (int, error) {
	switch x := v.(type) {
	case nil:
		return 0, nil
	case int:
		return x, nil
	case int64:
		return int(x), nil
	case float64:
		return int(x), nil
	case string:
		if x == "" {
			return 0, nil
		}
		return strconv.Atoi(x)
	default:
		return 0, fmt.Errorf("expected number or string, got %T", v)
	}
}

// portShift records that one service's host port walked from old → new.
// Used to drive override-file emission and log output.
type portShift struct {
	Service  string
	Old      int
	New      int
	Protocol string
	HostIP   string
}

// walkComposeServices probes each service's host-published ports, walking
// +1 on EADDRINUSE up to maxAttempts. Returns the updated services (with
// new HostPort values) and the list of shifts (one entry per port that
// actually moved). Services with no walking required come back with their
// Ports unchanged.
//
// Probe-then-bind has a tiny race window where docker compose itself
// could lose to another binder between probe and `up`; in practice the
// window is microseconds and `compose up` will surface its own error if
// it loses, which the user can react to.
func walkComposeServices(services []composeService, maxAttempts int, log *slog.Logger) ([]composeService, []portShift) {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	updated := make([]composeService, len(services))
	var shifts []portShift
	for i, svc := range services {
		updated[i] = composeService{Name: svc.Name, Ports: append([]composePortBinding(nil), svc.Ports...)}
		for j, p := range svc.Ports {
			if p.HostPort == 0 {
				// Random/dynamic — docker picks at up time.
				continue
			}
			host := p.HostIP
			if host == "" {
				host = "127.0.0.1"
			}
			actual, err := probeFreePort(host, p.HostPort, maxAttempts, log)
			if err != nil {
				// Walking disabled (max=1) or every port in range busy:
				// leave the original value and let `compose up` fail
				// with its own error. The earlier WARN logs already told
				// the user what happened.
				continue
			}
			updated[i].Ports[j].HostPort = actual
			if actual != p.HostPort {
				shifts = append(shifts, portShift{
					Service:  svc.Name,
					Old:      p.HostPort,
					New:      actual,
					Protocol: p.Protocol,
					HostIP:   p.HostIP,
				})
			}
		}
	}
	return updated, shifts
}

// renderShortPort renders a composePortBinding back into compose short
// form: "[host_ip:][host:]container[/proto]". Used to write override
// files in the same format the scaffold uses, so a developer who opens
// `.hamr/compose.<name>.override.yaml` recognises the layout.
func renderShortPort(b composePortBinding) string {
	var sb strings.Builder
	if b.HostIP != "" {
		sb.WriteString(b.HostIP)
		sb.WriteByte(':')
	}
	if b.HostPort != 0 {
		sb.WriteString(strconv.Itoa(b.HostPort))
		sb.WriteByte(':')
	}
	sb.WriteString(strconv.Itoa(b.Container))
	if b.Protocol != "" {
		sb.WriteByte('/')
		sb.WriteString(b.Protocol)
	}
	return sb.String()
}

// writeComposeOverride writes a YAML override file to path that replaces
// each affected service's `ports:` list (using the !reset tag) with the
// rewritten bindings. Only services whose names appear in affected are
// emitted — unaffected services keep their original ports via the base
// compose file's merge.
//
// Compose's default merge concatenates list fields, which is exactly the
// wrong thing for `ports:` (we'd end up publishing both the original and
// the walked port). The !reset tag clears the inherited value so the
// override's list replaces it.
func writeComposeOverride(path string, services []composeService, affected map[string]bool) error {
	if len(affected) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}

	root := &yaml.Node{Kind: yaml.MappingNode}
	servicesNode := &yaml.Node{Kind: yaml.MappingNode}
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "services"},
		servicesNode,
	)
	for _, svc := range services {
		if !affected[svc.Name] {
			continue
		}
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!reset"}
		for _, p := range svc.Ports {
			seq.Content = append(seq.Content, &yaml.Node{
				Kind:  yaml.ScalarNode,
				Value: renderShortPort(p),
				Style: yaml.DoubleQuotedStyle,
			})
		}
		svcMap := &yaml.Node{Kind: yaml.MappingNode}
		svcMap.Content = append(svcMap.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "ports"},
			seq,
		)
		servicesNode.Content = append(servicesNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: svc.Name},
			svcMap,
		)
	}

	out, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("marshal override: %w", err)
	}
	header := []byte("# Generated by hamr dev — do not edit.\n# Replaces ports: lists for services whose host ports were walked due to\n# EADDRINUSE. Regenerated each time hamr dev starts.\n")
	if err := os.WriteFile(path, append(header, out...), 0o644); err != nil {
		return fmt.Errorf("write override %s: %w", path, err)
	}
	return nil
}

// composeOverridePath returns the path hamr writes the per-entry override
// to. Lives under .hamr/ so it sits alongside other dev artefacts and is
// easy for users to .gitignore (the scaffold's .gitignore already covers
// .hamr/).
func composeOverridePath(entryName string) string {
	return filepath.Join(".hamr", "compose."+entryName+".override.yaml")
}
