package devserver

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// decodeBracketForm parses Stripe's bracket-notation form encoding into a
// nested generic structure. Stripe-go encodes structs and slices like:
//
//	line_items[0][price_data][currency]=gbp
//	metadata[user_id]=42
//
// The result is a map[string]any whose values are strings (leaves), nested
// map[string]any (object segments), or []any (numeric-index segments).
// Numeric bracket segments produce slices; non-numeric produce maps.
//
// Decoding is permissive: when the same key receives multiple values (rare
// in practice — stripe-go always uses indexed slice elements) the last
// value wins. Empty-bracket "[]" notation is treated as append-to-slice for
// the rare cases where stripe-go emits it.
func decodeBracketForm(values url.Values) (map[string]any, error) {
	root := map[string]any{}
	for key, vals := range values {
		segs, err := splitBracketKey(key)
		if err != nil {
			return nil, fmt.Errorf("decode key %q: %w", key, err)
		}
		for _, val := range vals {
			if err := setBracketPath(root, segs, val); err != nil {
				return nil, fmt.Errorf("set %q: %w", key, err)
			}
		}
	}
	return root, nil
}

// splitBracketKey turns "a[b][0][c]" into ["a", "b", "0", "c"]. The first
// segment must not be bracketed; subsequent segments are everything between
// matched "[" and "]". Empty brackets ("[]") yield "" — treated as append.
func splitBracketKey(key string) ([]string, error) {
	if key == "" {
		return nil, fmt.Errorf("empty key")
	}
	open := strings.IndexByte(key, '[')
	if open < 0 {
		return []string{key}, nil
	}
	out := []string{key[:open]}
	rest := key[open:]
	for len(rest) > 0 {
		if rest[0] != '[' {
			return nil, fmt.Errorf("expected '[' at %q", rest)
		}
		close := strings.IndexByte(rest, ']')
		if close < 0 {
			return nil, fmt.Errorf("unclosed bracket in %q", rest)
		}
		out = append(out, rest[1:close])
		rest = rest[close+1:]
	}
	return out, nil
}

// setBracketPath walks/creates intermediate containers along segs and writes
// val at the leaf. If a segment is numeric it implies a slice; otherwise a
// map. Empty segment ("[]") appends to the parent slice (creating one if the
// parent is empty).
func setBracketPath(root map[string]any, segs []string, val string) error {
	if len(segs) == 0 {
		return fmt.Errorf("no segments")
	}
	if len(segs) == 1 {
		root[segs[0]] = val
		return nil
	}

	// Walk into root[segs[0]] which must be a map (the top-level key is
	// always a struct/map field name in Stripe's encoding).
	var cur any = root
	for i := 0; i < len(segs)-1; i++ {
		seg := segs[i]
		next := segs[i+1]
		nextIsIndex := isNumeric(next) || next == ""

		switch parent := cur.(type) {
		case map[string]any:
			child, ok := parent[seg]
			if !ok {
				child = newContainer(nextIsIndex)
				parent[seg] = child
			}
			cur = child
		case []any:
			idx, err := indexFromSegment(seg, len(parent))
			if err != nil {
				return err
			}
			// Grow slice if needed.
			for len(parent) <= idx {
				parent = append(parent, nil)
			}
			child := parent[idx]
			if child == nil {
				child = newContainer(nextIsIndex)
				parent[idx] = child
			}
			// Reassign back into the parent container — slices are values.
			if err := writeBack(root, segs[:i], parent); err != nil {
				return err
			}
			cur = child
		default:
			return fmt.Errorf("conflicting path at segment %q (parent is %T, expected container)", seg, cur)
		}
	}

	// Set the leaf.
	leafSeg := segs[len(segs)-1]
	switch parent := cur.(type) {
	case map[string]any:
		parent[leafSeg] = val
		return nil
	case []any:
		idx, err := indexFromSegment(leafSeg, len(parent))
		if err != nil {
			return err
		}
		for len(parent) <= idx {
			parent = append(parent, nil)
		}
		parent[idx] = val
		return writeBack(root, segs[:len(segs)-1], parent)
	default:
		return fmt.Errorf("cannot write leaf at %q (parent %T)", leafSeg, cur)
	}
}

// writeBack reattaches a (possibly grown) slice back into its parent
// container, since Go slice append may produce a new backing array. path
// is the segments leading TO the slice (not including its own segment).
func writeBack(root map[string]any, path []string, slice []any) error {
	if len(path) == 0 {
		return fmt.Errorf("cannot writeBack with empty path")
	}
	var cur any = root
	for i := 0; i < len(path)-1; i++ {
		switch parent := cur.(type) {
		case map[string]any:
			cur = parent[path[i]]
		case []any:
			idx, err := indexFromSegment(path[i], len(parent))
			if err != nil {
				return err
			}
			cur = parent[idx]
		default:
			return fmt.Errorf("writeBack: unexpected container %T", cur)
		}
	}
	last := path[len(path)-1]
	switch parent := cur.(type) {
	case map[string]any:
		parent[last] = slice
		return nil
	case []any:
		idx, err := indexFromSegment(last, len(parent))
		if err != nil {
			return err
		}
		parent[idx] = slice
		return nil
	default:
		return fmt.Errorf("writeBack: unexpected leaf parent %T", cur)
	}
}

func newContainer(isIndex bool) any {
	if isIndex {
		return []any{}
	}
	return map[string]any{}
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// indexFromSegment returns the integer position for a segment used to index
// into a slice. Empty segment means append-at-end.
func indexFromSegment(seg string, currentLen int) (int, error) {
	if seg == "" {
		return currentLen, nil
	}
	n, err := strconv.Atoi(seg)
	if err != nil {
		return 0, fmt.Errorf("non-numeric slice index %q", seg)
	}
	if n < 0 {
		return 0, fmt.Errorf("negative slice index %d", n)
	}
	return n, nil
}
