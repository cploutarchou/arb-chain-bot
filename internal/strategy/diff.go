package strategy

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Change records one leaf-level difference between two payloads.
type Change struct {
	Old any `json:"old"`
	New any `json:"new"`
}

// Diff flattens both payloads to dotted JSON paths and reports every leaf
// that differs. An empty result means the payloads are identical.
func Diff(oldP, newP Params) (map[string]Change, error) {
	oldFlat, err := flatten(oldP)
	if err != nil {
		return nil, err
	}
	newFlat, err := flatten(newP)
	if err != nil {
		return nil, err
	}
	out := map[string]Change{}
	for k, ov := range oldFlat {
		nv, ok := newFlat[k]
		if !ok {
			out[k] = Change{Old: ov, New: nil}
			continue
		}
		if fmt.Sprint(ov) != fmt.Sprint(nv) {
			out[k] = Change{Old: ov, New: nv}
		}
	}
	for k, nv := range newFlat {
		if _, ok := oldFlat[k]; !ok {
			out[k] = Change{Old: nil, New: nv}
		}
	}
	return out, nil
}

// TopLevelSections returns the sorted set of first path segments touched
// by a diff ("risk", "scanner", ...); the API maps sections to the RBAC
// permission required to change them.
func TopLevelSections(diff map[string]Change) []string {
	seen := map[string]bool{}
	for path := range diff {
		if i := strings.IndexByte(path, '.'); i > 0 {
			seen[path[:i]] = true
		} else {
			seen[path] = true
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// ApplyChange returns a copy of p with the leaf at the dotted path set
// to value (JSON-typed: numbers for int fields, strings for decimals).
// Unknown paths and payloads that fail Validate are rejected — this is
// the only way AI recommendations become parameters, so the same
// validation gate applies to them as to human edits.
func ApplyChange(p Params, path string, value any) (Params, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return Params{}, err
	}
	var tree map[string]any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return Params{}, err
	}
	segs := strings.Split(path, ".")
	node := tree
	for i, seg := range segs {
		if i == len(segs)-1 {
			if _, ok := node[seg]; !ok {
				return Params{}, fmt.Errorf("%w: unknown parameter %q", ErrInvalid, path)
			}
			node[seg] = value
			break
		}
		child, ok := node[seg].(map[string]any)
		if !ok {
			return Params{}, fmt.Errorf("%w: unknown parameter %q", ErrInvalid, path)
		}
		node = child
	}
	mutated, err := json.Marshal(tree)
	if err != nil {
		return Params{}, err
	}
	var out Params
	dec := json.NewDecoder(strings.NewReader(string(mutated)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return Params{}, fmt.Errorf("%w: %s", ErrInvalid, err)
	}
	if err := out.Validate(); err != nil {
		return Params{}, fmt.Errorf("%w: %s", ErrInvalid, err)
	}
	return out, nil
}

func flatten(p Params) (map[string]any, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var tree map[string]any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil, err
	}
	out := map[string]any{}
	flattenInto(out, "", tree)
	return out, nil
}

func flattenInto(out map[string]any, prefix string, v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, cv := range t {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			flattenInto(out, key, cv)
		}
	case []any:
		// Lists compare as one JSON value: order matters (routes).
		b, _ := json.Marshal(t)
		out[prefix] = string(b)
	default:
		out[prefix] = t
	}
}
