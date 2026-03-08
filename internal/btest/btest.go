// Package btest implements the beecon test assertion framework.
// Test files use the .beecon-test extension and contain assertions
// evaluated against PlanResult.
//
// Syntax:
//
//	assert <node-name> <field> <op> <value>
//	assert_count <operation> <count>
//
// Operations: ==, !=, contains
//
// Example:
//
//	assert api intent.engine == "ecs"
//	assert db intent.storage_gib == "100"
//	assert_count CREATE 2
//	assert_count DELETE 0
package btest

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/terracotta-ai/beecon/internal/engine"
	"github.com/terracotta-ai/beecon/internal/ir"
)

// Assertion is a single test assertion.
type Assertion struct {
	Line    int    `json:"line"`
	Raw     string `json:"raw"`
	Type    string `json:"type"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

// TestResult is the outcome of running a test file.
type TestResult struct {
	Path       string      `json:"path"`
	Assertions []Assertion `json:"assertions"`
	Passed     int         `json:"passed"`
	Failed     int         `json:"failed"`
}

// RunFile parses and evaluates a .beecon-test file against a PlanResult.
func RunFile(path string, res *engine.PlanResult) (*TestResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	result := &TestResult{Path: path}
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		a := Assertion{Line: lineNum, Raw: line}
		parts := tokenize(line)
		if len(parts) == 0 {
			continue
		}

		switch parts[0] {
		case "assert":
			a.Type = "assert"
			evaluateAssert(&a, parts[1:], res)
		case "assert_count":
			a.Type = "assert_count"
			evaluateAssertCount(&a, parts[1:], res)
		default:
			a.Type = parts[0]
			a.Message = fmt.Sprintf("unknown assertion type: %s", parts[0])
		}

		if a.Passed {
			result.Passed++
		} else {
			result.Failed++
		}
		result.Assertions = append(result.Assertions, a)
	}
	return result, scanner.Err()
}

// evaluateAssert handles: assert <node-name> <field> <op> <value>
func evaluateAssert(a *Assertion, args []string, res *engine.PlanResult) {
	if len(args) < 4 {
		a.Message = "assert requires: <node-name> <field> <op> <value>"
		return
	}
	nodeName := args[0]
	field := args[1]
	op := args[2]
	expected := unquote(args[3])

	node := findNode(res, nodeName)
	if node == nil {
		a.Message = fmt.Sprintf("node %q not found", nodeName)
		return
	}

	actual := resolveField(node, field)

	switch op {
	case "==":
		a.Passed = actual == expected
		if !a.Passed {
			a.Message = fmt.Sprintf("%s.%s: got %q, expected %q", nodeName, field, actual, expected)
		}
	case "!=":
		a.Passed = actual != expected
		if !a.Passed {
			a.Message = fmt.Sprintf("%s.%s: got %q, expected != %q", nodeName, field, actual, expected)
		}
	case "contains":
		a.Passed = strings.Contains(actual, expected)
		if !a.Passed {
			a.Message = fmt.Sprintf("%s.%s: %q does not contain %q", nodeName, field, actual, expected)
		}
	default:
		a.Message = fmt.Sprintf("unknown operator %q (use ==, !=, contains)", op)
	}
}

// evaluateAssertCount handles: assert_count <operation> <count>
func evaluateAssertCount(a *Assertion, args []string, res *engine.PlanResult) {
	if len(args) < 2 {
		a.Message = "assert_count requires: <operation> <count>"
		return
	}
	operation := strings.ToUpper(args[0])
	expected, err := strconv.Atoi(args[1])
	if err != nil {
		a.Message = fmt.Sprintf("invalid count %q: %v", args[1], err)
		return
	}

	actual := 0
	for _, act := range res.Plan.Actions {
		if act.Operation == operation {
			actual++
		}
	}
	a.Passed = actual == expected
	if !a.Passed {
		a.Message = fmt.Sprintf("%s count: got %d, expected %d", operation, actual, expected)
	}
}

func findNode(res *engine.PlanResult, name string) *ir.IntentNode {
	// Search by name first, then by ID
	node := res.Graph.NodeByName(name)
	if node != nil {
		return node
	}
	for i := range res.Graph.Nodes {
		if res.Graph.Nodes[i].ID == name {
			return &res.Graph.Nodes[i]
		}
	}
	return nil
}

func resolveField(node *ir.IntentNode, field string) string {
	switch {
	case strings.HasPrefix(field, "intent."):
		return node.Intent[strings.TrimPrefix(field, "intent.")]
	case strings.HasPrefix(field, "performance."):
		return node.Performance[strings.TrimPrefix(field, "performance.")]
	case strings.HasPrefix(field, "env."):
		return node.Env[strings.TrimPrefix(field, "env.")]
	case field == "name":
		return node.Name
	case field == "type":
		return string(node.Type)
	default:
		return node.Intent[field]
	}
}

// tokenize splits a line respecting quoted strings.
func tokenize(line string) []string {
	var tokens []string
	var current strings.Builder
	inQuote := false
	quoteChar := byte(0)

	for i := 0; i < len(line); i++ {
		c := line[i]
		if inQuote {
			if c == quoteChar {
				inQuote = false
				current.WriteByte(c)
			} else {
				current.WriteByte(c)
			}
		} else if c == '"' || c == '\'' {
			inQuote = true
			quoteChar = c
			current.WriteByte(c)
		} else if c == ' ' || c == '\t' {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		} else {
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}
