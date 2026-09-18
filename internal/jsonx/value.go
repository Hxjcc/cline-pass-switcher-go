// Package jsonx extracts typed values from decoded JSON without coercion.
package jsonx

func Map(value any) map[string]any { result, _ := value.(map[string]any); return result }
func Slice(value any) []any        { result, _ := value.([]any); return result }
func String(value any) string      { result, _ := value.(string); return result }
