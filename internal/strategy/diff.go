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
