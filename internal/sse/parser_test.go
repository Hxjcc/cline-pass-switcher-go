package sse

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestFramingAtEveryChunkBoundary(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n", "\r"} {
		wire := "data: one" + ending + "data: two" + ending + ending + ": ping" + ending + ending + "data: tail"
		for split := 0; split <= len(wire); split++ {
			var p Parser
			var blocks []string
			emit := func(block string) bool { blocks = append(blocks, block); return true }
			if err := p.Feed([]byte(wire[:split]), emit); err != nil {
				t.Fatal(err)
			}
			if err := p.Feed([]byte(wire[split:]), emit); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(blocks, []string{"data: one\ndata: two", ": ping"}) || p.Finish() != "data: tail" {
				t.Fatalf("ending %q split %d: %q", ending, split, blocks)
			}
		}
	}
}

func TestUnterminatedEventIsBounded(t *testing.T) {
	var p Parser
	chunk := []byte(strings.Repeat("x", 32<<10))
	var err error
	for i := 0; i <= MaxEventBytes/len(chunk); i++ {
		err = p.Feed(chunk, func(string) bool { t.Fatal("unexpected event"); return true })
		if err != nil {
			break
		}
	}
	if !errors.Is(err, ErrEventTooLarge) || len(p.buf) != 0 {
		t.Fatalf("err=%v buffered=%d", err, len(p.buf))
	}
	if !errors.Is(p.Feed([]byte("\n\n"), func(string) bool { return true }), ErrEventTooLarge) {
		t.Fatal("overflow must remain terminal")
	}
}

func TestLimitAppliesToEventNotEntireStream(t *testing.T) {
	var p Parser
	chunk := []byte("data: " + strings.Repeat("x", 32<<10) + "\n\n")
	count := 0
	for i := 0; i < 300; i++ {
		if err := p.Feed(chunk, func(string) bool { count++; return true }); err != nil {
			t.Fatal(err)
		}
	}
	if count != 300 {
		t.Fatal(count)
	}
}

func BenchmarkFragmentedEvent(b *testing.B) {
	chunk := []byte(strings.Repeat("x", 1024))
	b.ReportAllocs()
	for b.Loop() {
		var p Parser
		for i := 0; i < 256; i++ {
			_ = p.Feed(chunk, func(string) bool { return true })
		}
	}
}
