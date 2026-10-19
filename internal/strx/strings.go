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
	if limit <= 0 {
		return ""
	}
	// Walk runes in place. Converting the whole string to []rune first made a
	// 64 MiB upstream error allocate ~256 MiB just to keep the first 160
	// characters.
	count := 0
	for index := range value {
		if count == limit {
			return value[:index]
		}
		count++
	}
	return value
}
