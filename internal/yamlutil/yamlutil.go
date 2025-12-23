package yamlutil

import (
	"fmt"
	"strconv"
	"strings"

	yaml "github.com/goccy/go-yaml"
)

// File represents a reversible YAML document: decoded value + comment sidecar.
type File struct {
	Value any
	CM    yaml.CommentMap
}

func ParseBytes(b []byte) (*File, error) {
	cm := yaml.CommentMap{}
	var v any
	if err := yaml.UnmarshalWithOptions(
		b,
		&v,
		yaml.CommentToMap(cm),
		yaml.UseOrderedMap(),
	); err != nil {
		return nil, err
	}
	return &File{Value: v, CM: cm}, nil
}

// Render re-encodes YAML while re-injecting comments captured in CM.
func Render(f *File) (string, error) {
	out, err := yaml.MarshalWithOptions(
		f.Value,
		yaml.WithComment(f.CM),
		// Optional: yaml.Indent(2), if you want stable indentation
	)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// GetString reads a scalar value at yamlPath and returns it as a string.
func GetString(f *File, yamlPath string) (string, bool, error) {
	p, err := yaml.PathString(yamlPath)
	if err != nil {
		return "", false, err
	}
	var out any
	// Filter marshals the target internally, then applies YAMLPath read.
	if err := p.Filter(f.Value, &out); err != nil {
		return "", false, nil
	}
	if out == nil {
		return "", false, nil
	}
	switch x := out.(type) {
	case string:
		return x, true, nil
	default:
		return fmt.Sprint(x), true, nil
	}
}

// SetString sets a scalar string at yamlPath by mutating the decoded object graph.
// Returns whether it changed.
func SetString(f *File, yamlPath string, newValue string) (bool, error) {
	cur, ok, _ := GetString(f, yamlPath)
	if ok && cur == newValue {
		return false, nil
	}

	steps, err := parseSimpleYAMLPath(yamlPath)
	if err != nil {
		return false, err
	}

	if err := setAtPath(&f.Value, steps, newValue); err != nil {
		return false, err
	}
	return true, nil
}

type pathStep struct {
	key   *string
	index *int
}

// parseSimpleYAMLPath supports the subset we use in Chart.yaml:
//
//	$.key
//	$.key.child
//	$.arr[0].key
func parseSimpleYAMLPath(p string) ([]pathStep, error) {
	if p == "$" {
		return nil, fmt.Errorf("path refers to root; expected $.key: %q", p)
	}
	if !strings.HasPrefix(p, "$.") {
		return nil, fmt.Errorf("unsupported path (expected to start with $.): %q", p)
	}
	p = strings.TrimPrefix(p, "$.")
	parts := strings.Split(p, ".")
	steps := make([]pathStep, 0, len(parts))

	for _, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("invalid path segment in %q", p)
		}

		// segment can be: "foo" or "foo[3]" or "[3]" (we don't expect bare indexes here)
		for {
			br := strings.IndexByte(part, '[')
			if br == -1 {
				k := part
				steps = append(steps, pathStep{key: &k})
				break
			}

			// key before [
			if br > 0 {
				k := part[:br]
				steps = append(steps, pathStep{key: &k})
			}

			end := strings.IndexByte(part[br:], ']')
			if end == -1 {
				return nil, fmt.Errorf("unclosed index in segment %q", part)
			}
			end += br

			idxStr := part[br+1 : end]
			idx, err := strconv.Atoi(idxStr)
			if err != nil {
				return nil, fmt.Errorf("invalid index %q in segment %q", idxStr, part)
			}
			steps = append(steps, pathStep{index: &idx})

			// remainder after ]
			if end+1 >= len(part) {
				break
			}
			part = part[end+1:]
			if part == "" {
				break
			}
		}
	}
	return steps, nil
}

func setAtPath(root *any, steps []pathStep, newValue string) error {
	var cur any = *root

	// Walk to parent of leaf
	for i := 0; i < len(steps)-1; i++ {
		s := steps[i]
		next := steps[i+1]

		switch {
		case s.key != nil:
			ms, ok := cur.(yaml.MapSlice)
			if !ok {
				return fmt.Errorf("expected map at step %d (%q), got %T", i, *s.key, cur)
			}
			child, ok := mapSliceGet(ms, *s.key)
			if !ok {
				return fmt.Errorf("key not found: %q", *s.key)
			}
			cur = child

			// If next is an index, child must be a slice; continue.
			_ = next

		case s.index != nil:
			arr, ok := cur.([]any)
			if !ok {
				// go-yaml often uses []interface{}; accept that too.
				if ai, ok2 := cur.([]interface{}); ok2 {
					arr = make([]any, len(ai))
					for j := range ai {
						arr[j] = ai[j]
					}
					ok = true
				}
			}
			if !ok {
				return fmt.Errorf("expected array at step %d ([%d]), got %T", i, *s.index, cur)
			}
			if *s.index < 0 || *s.index >= len(arr) {
				return fmt.Errorf("index out of range at step %d: %d", i, *s.index)
			}
			cur = arr[*s.index]
		default:
			return fmt.Errorf("invalid path step at %d", i)
		}
	}

	leaf := steps[len(steps)-1]
	switch {
	case leaf.key != nil:
		ms, ok := cur.(yaml.MapSlice)
		if !ok {
			return fmt.Errorf("expected map for leaf key %q, got %T", *leaf.key, cur)
		}
		changed := mapSliceSet(&ms, *leaf.key, newValue)
		if !changed {
			return nil
		}
		// write back: cur is a copy, so we need to update the parent container.
		// easiest: re-walk and assign (small charts), but we already have root pointer.
		// For Chart.yaml we only set top-level keys and known nested deps; implement
		// a simple re-assignment by rebuilding via setAtPathAssign below.
		return setAtPathAssign(root, steps[:len(steps)-1], ms)

	case leaf.index != nil:
		arr, ok := cur.([]any)
		if !ok {
			if ai, ok2 := cur.([]interface{}); ok2 {
				arr = make([]any, len(ai))
				for j := range ai {
					arr[j] = ai[j]
				}
				ok = true
			}
		}
		if !ok {
			return fmt.Errorf("expected array for leaf index [%d], got %T", *leaf.index, cur)
		}
		if *leaf.index < 0 || *leaf.index >= len(arr) {
			return fmt.Errorf("index out of range at leaf: %d", *leaf.index)
		}
		arr[*leaf.index] = newValue
		return setAtPathAssign(root, steps[:len(steps)-1], arr)

	default:
		return fmt.Errorf("invalid leaf step")
	}
}

// setAtPathAssign assigns a value back into the object graph at the given path.
func setAtPathAssign(root *any, steps []pathStep, value any) error {
	if len(steps) == 0 {
		*root = value
		return nil
	}

	// Walk to parent of target
	var cur any = *root
	for i := 0; i < len(steps)-1; i++ {
		s := steps[i]
		switch {
		case s.key != nil:
			ms := cur.(yaml.MapSlice)
			child, _ := mapSliceGet(ms, *s.key)
			cur = child
		case s.index != nil:
			arr := cur.([]any)
			cur = arr[*s.index]
		}
	}

	leaf := steps[len(steps)-1]
	switch {
	case leaf.key != nil:
		ms := cur.(yaml.MapSlice)
		mapSliceSet(&ms, *leaf.key, value)
		return setAtPathAssign(root, steps[:len(steps)-1], ms)
	case leaf.index != nil:
		arr := cur.([]any)
		arr[*leaf.index] = value
		return setAtPathAssign(root, steps[:len(steps)-1], arr)
	default:
		return fmt.Errorf("invalid assign leaf")
	}
}

func mapSliceGet(ms yaml.MapSlice, key string) (any, bool) {
	for _, it := range ms {
		if ks, ok := it.Key.(string); ok && ks == key {
			return it.Value, true
		}
	}
	return nil, false
}

// mapSliceSet sets key to value. Returns true if key existed or was added.
func mapSliceSet(ms *yaml.MapSlice, key string, value any) bool {
	for i := range *ms {
		if ks, ok := (*ms)[i].Key.(string); ok && ks == key {
			(*ms)[i].Value = value
			return true
		}
	}
	*ms = append(*ms, yaml.MapItem{Key: key, Value: value})
	return true
}
