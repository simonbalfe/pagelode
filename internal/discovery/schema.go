package discovery

import (
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const maxSchemaFields = 512

func parseJSON(body string) (any, bool) {
	var value any
	if err := json.Unmarshal([]byte(body), &value); err != nil {
		return nil, false
	}
	return value, true
}

func schema(body string) []Field {
	value, ok := parseJSON(body)
	if !ok {
		return []Field{}
	}
	fields := map[string]map[string]bool{}
	walkSchema(value, "$", 0, fields)
	result := make([]Field, 0, len(fields))
	for path, types := range fields {
		result = append(result, Field{Path: path, Types: sortedKeys(types), Evidence: []string{}})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result
}

func walkSchema(value any, path string, depth int, fields map[string]map[string]bool) {
	if depth > 8 || len(fields) >= maxSchemaFields {
		return
	}
	if fields[path] == nil {
		fields[path] = map[string]bool{}
	}
	fields[path][valueType(value)] = true
	switch value := value.(type) {
	case map[string]any:
		names := make([]string, 0, len(value))
		for name := range value {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			walkSchema(value[name], path+"."+safeName(name), depth+1, fields)
		}
	case []any:
		for i, item := range value {
			if i >= 20 {
				break
			}
			walkSchema(item, path+"[]", depth+1, fields)
		}
	}
}

func valueType(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	default:
		return "number"
	}
}

func mergeFields(existing []Field, incoming []Field, evidence string) []Field {
	for _, field := range incoming {
		found := false
		for i := range existing {
			if existing[i].Path == field.Path {
				for _, kind := range field.Types {
					existing[i].Types = appendUnique(existing[i].Types, kind)
				}
				existing[i].Evidence = appendUnique(existing[i].Evidence, evidence)
				found = true
				break
			}
		}
		if !found && len(existing) < maxSchemaFields {
			field.Evidence = []string{evidence}
			existing = append(existing, field)
		}
	}
	sort.Slice(existing, func(i, j int) bool { return existing[i].Path < existing[j].Path })
	return existing
}

func requestFields(body, contentType string) []Field {
	if fields := schema(body); len(fields) > 0 {
		return fields
	}
	if !strings.Contains(contentType, "application/x-www-form-urlencoded") {
		return []Field{}
	}
	values, err := url.ParseQuery(body)
	if err != nil {
		return []Field{}
	}
	return queryFields(values, "body.")
}

func queryFields(values url.Values, prefix string) []Field {
	result := make([]Field, 0, len(values))
	for name, values := range values {
		types := []string{}
		for _, value := range values {
			kind := "string"
			if _, err := strconv.ParseFloat(value, 64); err == nil {
				kind = "number"
			} else if value == "true" || value == "false" {
				kind = "boolean"
			}
			types = appendUnique(types, kind)
		}
		result = append(result, Field{Path: prefix + safeName(name), Types: types, Evidence: []string{}})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result
}

func sortedKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func safeName(value string) string {
	if len(value) > 100 {
		return "[redacted]"
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_-$.:[]", r)) {
			return "[redacted]"
		}
	}
	return value
}
