package ast

// Value is a scalar or list literal in Beecon.
type Value struct {
	Raw  string
	List []string
}

func (v Value) IsList() bool { return len(v.List) > 0 }

// Block is a parsed Beecon block. Kind is the declared keyword for top-level blocks
// (domain/service/store/network/compute/profile) or the nested block name for child blocks
// (boundary/performance/needs/env).
type Block struct {
	Kind     string
	Name     string
	Fields   map[string]Value
	Children []*Block
}

// File is a parsed .beecon file.
type File struct {
	Blocks []*Block
}
