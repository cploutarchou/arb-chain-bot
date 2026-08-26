package reporting

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// CSVRows flattens the report into a tabular (section, field, value)
// table for BL-32's CSV export: the console renders structured sections,
// not raw JSON, and a spreadsheet-friendly download needs one row per
// leaf value rather than nested objects. Struct fields recurse using
// their JSON tag name; slices/arrays get an index suffix ("[0]", "[1]",
// ...) on the section; the report's own top-level fields ARE the
// sections (system_health, opportunities, top_triangles[0], ...).
func (r Report) CSVRows() [][]string {
	rows := [][]string{{"section", "field", "value"}}
	v := reflect.ValueOf(r)
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue // unexported
		}
		name := jsonFieldName(f)
		if name == "-" || name == "" {
			continue
		}
		flattenCSV(name, v.Field(i), &rows)
	}
	return rows
}

// CSV renders CSVRows as RFC 4180 text/csv bytes. Values whose first
// character is one of = + - @ are prefixed with a leading apostrophe:
// report prose (e.g. incident titles, AI summaries) is free text that
// could otherwise be interpreted as a formula by a spreadsheet importer
// (CSV-injection defense in depth).
func (r Report) CSV() ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	for _, row := range r.CSVRows() {
		safe := make([]string, len(row))
		for i, cell := range row {
			safe[i] = neutralizeCSVFormula(cell)
		}
		if err := w.Write(safe); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func neutralizeCSVFormula(s string) string {
	if len(s) > 0 && strings.ContainsRune("=+-@", rune(s[0])) {
		return "'" + s
	}
	return s
}

// jsonFieldName reads the struct tag "json" name (before any comma
// option), falling back to the Go field name when the tag is absent.
func jsonFieldName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" {
		return f.Name
	}
	name := strings.SplitN(tag, ",", 2)[0]
	if name == "" {
		return f.Name
	}
	return name
}

var stringerType = reflect.TypeOf((*fmt.Stringer)(nil)).Elem()

func flattenCSV(path string, v reflect.Value, rows *[][]string) {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			section, field := splitCSVPath(path)
			*rows = append(*rows, []string{section, field, ""})
			return
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return
	}
	// Stringer types (time.Time, decimal.Decimal, ...) are leaves even
	// though their Kind is Struct.
	if v.Kind() == reflect.Struct && v.Type().Implements(stringerType) {
		section, field := splitCSVPath(path)
		*rows = append(*rows, []string{section, field, v.Interface().(fmt.Stringer).String()})
		return
	}
	switch v.Kind() {
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue
			}
			name := jsonFieldName(f)
			if name == "-" {
				continue
			}
			child := name
			if path != "" {
				child = path + "." + name
			}
			flattenCSV(child, v.Field(i), rows)
		}
	case reflect.Slice, reflect.Array:
		if v.Len() == 0 {
			section, field := splitCSVPath(path)
			*rows = append(*rows, []string{section, field, "(empty)"})
			return
		}
		for i := 0; i < v.Len(); i++ {
			flattenCSV(fmt.Sprintf("%s[%d]", path, i), v.Index(i), rows)
		}
	case reflect.Map:
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return fmt.Sprint(keys[i].Interface()) < fmt.Sprint(keys[j].Interface()) })
		if len(keys) == 0 {
			section, field := splitCSVPath(path)
			*rows = append(*rows, []string{section, field, "(empty)"})
			return
		}
		for _, k := range keys {
			flattenCSV(fmt.Sprintf("%s.%v", path, k.Interface()), v.MapIndex(k), rows)
		}
	default:
		section, field := splitCSVPath(path)
		*rows = append(*rows, []string{section, field, fmt.Sprint(v.Interface())})
	}
}

// splitCSVPath separates a dotted/indexed field path into its leading
// section (up to the first '.' — a following "[n]" stays attached to
// the section it indexes) and the remaining field path.
func splitCSVPath(path string) (section, field string) {
	if i := strings.IndexByte(path, '.'); i >= 0 {
		return path[:i], path[i+1:]
	}
	return path, ""
}
