package strx

import (
	"strings"
	"testing"
)

func TestTruncateCountsRunesWithoutCopyingTheWholeInput(t *testing.T) {
	if got := Truncate("你好世界", 2); got != "你好" {
		t.Fatalf("Truncate = %q", got)
	}
	if got := Truncate(strings.Repeat("x", 1<<20), 4); got != "xxxx" {
		t.Fatalf("Truncate = %q", got)
	}
	if got := Truncate("short", 10); got != "short" {
		t.Fatalf("Truncate = %q", got)
	}
}
