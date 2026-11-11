package lang

import (
	ts "github.com/tree-sitter/go-tree-sitter"
)

// minCaseTableEntries is the entry count at which a map of structured values
// reads as one record per case rather than data.
const kindCallExpression = "call_expression"

const minCaseTableEntries = 15

// CaseTable is a map literal that holds one structured record per case: a
// switch with a branch per entry.
type CaseTable struct {
	StartLine int `json:"start_line"`
	EndLine   int `json:"end_line"`
	Entries   int `json:"entries"`
}

// CaseTables finds the map literals with many structured entries (calls,
// tuples, nested literals).
func CaseTables(l *Language, root *ts.Node) []CaseTable {
	var tables []CaseTable
	EachNamed(root, func(n *ts.Node) {
		entryKind := mapEntryKind(l, n)
		if entryKind == "" {
			return
		}
		entries, structured := 0, 0
		for i := range n.NamedChildCount() {
			entry := n.NamedChild(i)
			if entry.Kind() != entryKind {
				continue
			}
			entries++
			if v := entryValue(entry); v != nil && structuredKinds[v.Kind()] {
				structured++
			}
		}
		if entries >= minCaseTableEntries && structured*3 >= entries*2 {
			tables = append(tables, CaseTable{
				StartLine: int(n.StartPosition().Row) + 1,
				EndLine:   int(n.EndPosition().Row) + 1,
				Entries:   entries,
			})
		}
	})

	return tables
}

// mapEntryKind answers the entry node kind of a map literal node, or "" for any other node.
func mapEntryKind(l *Language, n *ts.Node) string {
	switch {
	case l == Python && n.Kind() == "dictionary",
		(l == TypeScript || l == TSX) && n.Kind() == "object":
		return "pair"
	case l == Go && n.Kind() == "literal_value":
		if p := n.Parent(); p != nil && p.Kind() == "composite_literal" {
			if t := p.ChildByFieldName("type"); t != nil && t.Kind() == "map_type" {
				return "keyed_element"
			}
		}
	}

	return ""
}

// structuredKinds are the value nodes that make a map entry a record.
var structuredKinds = map[string]bool{ //nolint:gochecknoglobals // a fixed vocabulary
	"arrow_function": true, "array": true, "call": true, kindCallExpression: true,
	"composite_literal": true, "dictionary": true, "func_literal": true, "lambda": true,
	"list": true, "literal_value": true, "new_expression": true, "object": true,
	"set": true, "tuple": true,
}

// entryValue answers the value node of a map entry, unwrapping Go's literal_element.
func entryValue(entry *ts.Node) *ts.Node {
	v := entry.ChildByFieldName("value")
	if v != nil && v.Kind() == "literal_element" && v.NamedChildCount() > 0 {
		return v.NamedChild(0)
	}

	return v
}
