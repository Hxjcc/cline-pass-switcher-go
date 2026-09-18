package strx

import "strings"

// Unique preserves input order and whitespace, dropping empty strings.
func Unique(values []string) []string { return unique(values, false) }

// UniqueTrimmed trims each value before removing duplicates and empty strings.
func UniqueTrimmed(values []string) []string { return unique(values, true) }

func unique(values []string, trim bool) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if trim {
			value = strings.TrimSpace(value)
		}
		if value == "" {
			continue
		}
		if _, found := seen[value]; found {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func Truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
