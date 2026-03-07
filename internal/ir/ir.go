package ir

import (
	"fmt"
	"sort"
	"strings"

	"github.com/terracotta-ai/beecon/internal/ast"
)

// NodeType represents canonical Beecon intent node kinds.
type NodeType string

const (
	NodeService NodeType = "SERVICE"
	NodeStore   NodeType = "STORE"
	NodeNetwork NodeType = "NETWORK"
	NodeCompute NodeType = "COMPUTE"
)

// Dependency declares an edge from a node to another logical dependency.
type Dependency struct {
	Target string
	Mode   string
}

// IntentNode is the provider-agnostic intent unit.
type IntentNode struct {
	ID          string
	Name        string
	Type        NodeType
	Intent      map[string]string
	Performance map[string]string
	Needs       []Dependency
	Env         map[string]string
	Source      string
}

// DomainNode captures root constraints.
type DomainNode struct {
	Name       string
	Cloud      string
	Owner      string
	Compliance []string
	Boundary   map[string][]string
}

// Edge represents a dependency relation (From -> To).
type Edge struct {
	From string
	To   string
}

// Graph is the canonical intent graph.
type Graph struct {
	Nodes    []IntentNode
	Edges    []Edge
	Domain   *DomainNode
	Profiles map[string]Profile
}

// Profile is a reusable intent template.
type Profile struct {
	Name     string
	Fields   map[string]string
	Children map[string]map[string]string
}

func (g *Graph) NodesByID() map[string]IntentNode {
	out := make(map[string]IntentNode, len(g.Nodes))
	for _, n := range g.Nodes {
		out[n.ID] = n
	}
	return out
}

// Build converts AST into intent graph.
func Build(f *ast.File, source string) (*Graph, error) {
	g := &Graph{Profiles: map[string]Profile{}}
	nameToID := map[string]string{}

	for _, b := range f.Blocks {
		if b.Kind != "profile" {
			continue
		}
		p := Profile{
			Name:     b.Name,
			Fields:   map[string]string{},
			Children: map[string]map[string]string{},
		}
		for k, v := range b.Fields {
			p.Fields[k] = v.Raw
		}
		for _, c := range b.Children {
			if p.Children[c.Kind] == nil {
				p.Children[c.Kind] = map[string]string{}
			}
			for k, v := range c.Fields {
				p.Children[c.Kind][k] = v.Raw
			}
		}
		g.Profiles[p.Name] = p
	}

	for _, b := range f.Blocks {
		switch b.Kind {
		case "domain":
			d := &DomainNode{Name: b.Name, Boundary: map[string][]string{}}
			if v, ok := b.Fields["cloud"]; ok {
				d.Cloud = v.Raw
			}
			if v, ok := b.Fields["owner"]; ok {
				d.Owner = v.Raw
			}
			if v, ok := b.Fields["compliance"]; ok && v.IsList() {
				d.Compliance = append([]string{}, v.List...)
			}
			for _, c := range b.Children {
				if c.Kind != "boundary" {
					continue
				}
				for k, v := range c.Fields {
					if v.IsList() {
						d.Boundary[k] = append([]string{}, v.List...)
					} else {
						d.Boundary[k] = []string{v.Raw}
					}
				}
			}
			g.Domain = d
		case "service", "store", "network", "compute":
			node, err := buildIntentNode(b, source, g.Profiles)
			if err != nil {
				return nil, err
			}
			g.Nodes = append(g.Nodes, node)
			nameToID[b.Name] = node.ID
		}
	}

	for i := range g.Nodes {
		for _, dep := range g.Nodes[i].Needs {
			if depID, ok := nameToID[dep.Target]; ok {
				g.Edges = append(g.Edges, Edge{From: depID, To: g.Nodes[i].ID})
			}
		}
	}
	return g, nil
}

func buildIntentNode(b *ast.Block, source string, profiles map[string]Profile) (IntentNode, error) {
	nodeType, err := toNodeType(b.Kind)
	if err != nil {
		return IntentNode{}, err
	}
	n := IntentNode{
		ID:          strings.ToLower(fmt.Sprintf("%s.%s", b.Kind, b.Name)),
		Name:        b.Name,
		Type:        nodeType,
		Intent:      map[string]string{},
		Performance: map[string]string{},
		Env:         map[string]string{},
		Source:      source,
	}
	for _, ref := range profileRefs(b) {
		p, ok := profiles[ref]
		if !ok {
			continue
		}
		for k, v := range p.Fields {
			n.Intent[k] = v
		}
		if child := p.Children["performance"]; child != nil {
			for k, v := range child {
				n.Performance[k] = v
			}
		}
		if child := p.Children["env"]; child != nil {
			for k, v := range child {
				n.Env[k] = v
			}
		}
		if child := p.Children["needs"]; child != nil {
			keys := make([]string, 0, len(child))
			for depName := range child {
				keys = append(keys, depName)
			}
			sort.Strings(keys)
			for _, depName := range keys {
				n.Needs = append(n.Needs, Dependency{Target: depName, Mode: child[depName]})
			}
		}
	}
	for k, v := range b.Fields {
		n.Intent[k] = v.Raw
	}
	for _, c := range b.Children {
		switch c.Kind {
		case "performance":
			for k, v := range c.Fields {
				n.Performance[k] = v.Raw
			}
		case "needs":
			keys := make([]string, 0, len(c.Fields))
			for depName := range c.Fields {
				keys = append(keys, depName)
			}
			sort.Strings(keys)
			for _, depName := range keys {
				depMode := c.Fields[depName]
				n.Needs = append(n.Needs, Dependency{Target: depName, Mode: depMode.Raw})
			}
		case "env":
			for k, v := range c.Fields {
				n.Env[k] = v.Raw
			}
		}
	}
	return n, nil
}

func profileRefs(b *ast.Block) []string {
	v, ok := b.Fields["apply"]
	if !ok {
		return nil
	}
	if v.IsList() {
		return append([]string{}, v.List...)
	}
	if strings.HasPrefix(v.Raw, "[") && strings.HasSuffix(v.Raw, "]") {
		inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(v.Raw, "["), "]"))
		if inner == "" {
			return nil
		}
		parts := strings.Split(inner, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			item := strings.TrimSpace(strings.Trim(p, `"`))
			if item != "" {
				out = append(out, item)
			}
		}
		return out
	}
	if strings.TrimSpace(v.Raw) == "" {
		return nil
	}
	return []string{v.Raw}
}

func toNodeType(kind string) (NodeType, error) {
	switch kind {
	case "service":
		return NodeService, nil
	case "store":
		return NodeStore, nil
	case "network":
		return NodeNetwork, nil
	case "compute":
		return NodeCompute, nil
	default:
		return "", fmt.Errorf("unsupported node kind %q", kind)
	}
}
