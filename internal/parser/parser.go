package parser

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/terracotta-ai/beecon/internal/ast"
)

var topLevelBlockPattern = regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_]*)\s+([a-zA-Z_][a-zA-Z0-9_-]*)\s*\{$`)
var nestedBlockPattern = regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_]*)\s*\{$`)

var allowedTopLevelKinds = map[string]bool{
	"domain":  true,
	"service": true,
	"store":   true,
	"network": true,
	"compute": true,
	"profile": true,
}

// ParseFile parses a .beecon file into an AST.
func ParseFile(path string) (*ast.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	}
	defer f.Close()
	return Parse(f)
}

// Parse parses Beecon source into an AST.
func Parse(r io.Reader) (*ast.File, error) {
	s := bufio.NewScanner(r)
	lineNum := 0
	var root ast.File
	var stack []*ast.Block

	for s.Scan() {
		lineNum++
		line := stripComment(strings.TrimSpace(s.Text()))
		if line == "" {
			continue
		}

		switch {
		case line == "}":
			if len(stack) == 0 {
				return nil, fmt.Errorf("line %d: unexpected closing brace", lineNum)
			}
			stack = stack[:len(stack)-1]
		case strings.HasSuffix(line, "{"):
			blk, err := parseBlockHeader(line, len(stack) == 0)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNum, err)
			}
			if len(stack) == 0 {
				root.Blocks = append(root.Blocks, blk)
			} else {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, blk)
			}
			stack = append(stack, blk)
		default:
			if len(stack) == 0 {
				return nil, fmt.Errorf("line %d: assignment outside block", lineNum)
			}
			k, v, err := parseAssignment(line)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNum, err)
			}
			curr := stack[len(stack)-1]
			if curr.Fields == nil {
				curr.Fields = map[string]ast.Value{}
			}
			if _, exists := curr.Fields[k]; exists {
				return nil, fmt.Errorf("line %d: duplicate key %q", lineNum, k)
			}
			curr.Fields[k] = v
		}
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if len(stack) != 0 {
		return nil, fmt.Errorf("unclosed block: %s", stack[len(stack)-1].Kind)
	}
	if err := Validate(root); err != nil {
		return nil, err
	}
	return &root, nil
}

// Validate enforces semantic rules expected by Beecon.
func Validate(f ast.File) error {
	var errs []string
	if len(f.Blocks) == 0 {
		return fmt.Errorf("no blocks found")
	}
	domainCount := 0
	nodeNames := map[string]string{}
	profileNames := map[string]bool{}

	for _, b := range f.Blocks {
		if !allowedTopLevelKinds[b.Kind] {
			errs = append(errs, fmt.Sprintf("unsupported block type %q", b.Kind))
			continue
		}
		if b.Kind == "domain" {
			domainCount++
			if _, ok := b.Fields["cloud"]; !ok {
				errs = append(errs, "domain block missing required field 'cloud'")
			}
			if _, ok := b.Fields["owner"]; !ok {
				errs = append(errs, "domain block missing required field 'owner'")
			}
		}
		if b.Kind == "service" || b.Kind == "store" || b.Kind == "network" || b.Kind == "compute" {
			if prev, ok := nodeNames[b.Name]; ok {
				errs = append(errs, fmt.Sprintf("duplicate node name %q used by %s and %s", b.Name, prev, b.Kind))
			} else {
				nodeNames[b.Name] = b.Kind
			}
		}
		if b.Kind == "profile" {
			if profileNames[b.Name] {
				errs = append(errs, fmt.Sprintf("duplicate profile name %q", b.Name))
			}
			profileNames[b.Name] = true
		}
		for _, c := range b.Children {
			if c.Kind == "needs" && b.Kind != "service" && b.Kind != "compute" && b.Kind != "profile" {
				errs = append(errs, fmt.Sprintf("%s.%s: needs block only allowed in service/compute", b.Kind, b.Name))
			}
			if c.Kind == "performance" && b.Kind != "service" && b.Kind != "compute" && b.Kind != "profile" {
				errs = append(errs, fmt.Sprintf("%s.%s: performance block only allowed in service/compute", b.Kind, b.Name))
			}
		}
	}

	if domainCount != 1 {
		errs = append(errs, fmt.Sprintf("expected exactly 1 domain block, got %d", domainCount))
	}

	for _, b := range f.Blocks {
		if b.Kind == "service" || b.Kind == "store" || b.Kind == "network" || b.Kind == "compute" {
			for _, ref := range profileRefs(b) {
				if !profileNames[ref] {
					errs = append(errs, fmt.Sprintf("%s.%s references unknown profile %q", b.Kind, b.Name, ref))
				}
			}
		}
		if b.Kind != "service" && b.Kind != "compute" {
			continue
		}
		for _, c := range b.Children {
			if c.Kind != "needs" {
				continue
			}
			for dep := range c.Fields {
				if dep == b.Name {
					errs = append(errs, fmt.Sprintf("%s.%s has self-dependency", b.Kind, b.Name))
					continue
				}
				if _, ok := nodeNames[dep]; !ok {
					errs = append(errs, fmt.Sprintf("%s.%s needs unknown dependency %q", b.Kind, b.Name, dep))
				}
			}
		}
	}

	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("validation failed:\n- %s", strings.Join(errs, "\n- "))
	}
	return nil
}

func stripComment(line string) string {
	inQuote := false
	for i := 0; i < len(line); i++ {
		if line[i] == '\\' && inQuote && i+1 < len(line) {
			i++ // skip escaped character
			continue
		}
		if line[i] == '"' {
			inQuote = !inQuote
		}
		if line[i] == '#' && !inQuote {
			return strings.TrimSpace(line[:i])
		}
	}
	return line
}

func parseBlockHeader(line string, topLevel bool) (*ast.Block, error) {
	if topLevel {
		m := topLevelBlockPattern.FindStringSubmatch(line)
		if len(m) == 3 {
			return &ast.Block{Kind: strings.ToLower(m[1]), Name: m[2], Fields: map[string]ast.Value{}}, nil
		}
		return nil, fmt.Errorf("invalid top-level block header %q", line)
	}
	m := nestedBlockPattern.FindStringSubmatch(line)
	if len(m) == 2 {
		return &ast.Block{Kind: strings.ToLower(m[1]), Fields: map[string]ast.Value{}}, nil
	}
	return nil, fmt.Errorf("invalid nested block header %q", line)
}

func parseAssignment(line string) (string, ast.Value, error) {
	idx := strings.Index(line, "=")
	if idx == -1 {
		return "", ast.Value{}, fmt.Errorf("expected key = value")
	}
	k := strings.TrimSpace(line[:idx])
	if k == "" {
		return "", ast.Value{}, fmt.Errorf("empty key")
	}
	raw := strings.TrimSpace(line[idx+1:])
	if raw == "" {
		return "", ast.Value{}, fmt.Errorf("empty value")
	}
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]"))
		if inner == "" {
			return k, ast.Value{Raw: raw, List: []string{}}, nil
		}
		parts := splitListItems(inner)
		list := make([]string, 0, len(parts))
		for _, p := range parts {
			item := strings.Trim(strings.TrimSpace(p), `"`)
			if item != "" {
				list = append(list, item)
			}
		}
		return k, ast.Value{Raw: raw, List: list}, nil
	}
	return k, ast.Value{Raw: strings.Trim(raw, `"`)}, nil
}

// splitListItems splits a comma-separated string, respecting quoted segments
// and backslash escapes inside quotes. e.g. `"a,b", c` → ["a,b", "c"].
func splitListItems(s string) []string {
	var items []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '\\' && inQuote && i+1 < len(s) {
			cur.WriteByte(s[i+1])
			i++
			continue
		}
		if ch == '"' {
			inQuote = !inQuote
			cur.WriteByte(ch)
			continue
		}
		if ch == ',' && !inQuote {
			items = append(items, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(ch)
	}
	items = append(items, cur.String())
	return items
}

func profileRefs(b *ast.Block) []string {
	v, ok := b.Fields["apply"]
	if !ok {
		return nil
	}
	if v.IsList() {
		return append([]string{}, v.List...)
	}
	if strings.TrimSpace(v.Raw) == "" {
		return nil
	}
	return []string{v.Raw}
}
