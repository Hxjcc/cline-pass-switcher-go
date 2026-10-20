package strx

import (
	"strings"
	"testing"
	"unsafe"
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

func TestTruncateDoesNotRetainLargeInputBuffer(t *testing.T) {
	input := strings.Repeat("x", 1<<20)
	truncated := Truncate(input, 2000)
	// History and trace strings can live for hundreds of subsequent requests.
	// Compare ownership directly rather than relying on GC/heap thresholds.
	if unsafe.StringData(truncated) == unsafe.StringData(input) {
		t.Fatal("the truncated error still retains the original 1 MiB buffer")
	}
}
