package responses

import (
	"slices"
	"strings"
	"testing"
)

// The anchored-section check only feeds an advisory history note, so it must be
// generous: heading markup, casing and separator variants all count as present,
// while a summary that genuinely dropped a section is reported.
func TestMissingCompactionSections(t *testing.T) {
	complete := `## Objective
ship the parser fix

## Work State
parse.go:42 updated

## Next Move
rerun go test ./internal/parse

## Relevant Files
internal/parse.go`
	if missing := MissingCompactionSections(complete); len(missing) != 0 {
		t.Fatalf("complete summary flagged: %#v", missing)
	}

	// Models dress headings up: bold, list bullets, different casing and
	// separators. None of that should read as a missing section.
	dressed := "**Objective**: ship it\n- work_state — done\n### Next-Move\nrelevantFiles: internal/parse.go"
	if missing := MissingCompactionSections(dressed); len(missing) != 0 {
		t.Fatalf("decorated headings flagged: %#v", missing)
	}

	// Chinese headings are the fallback spellings from the alias table.
	chinese := "## 目标\n修好解析器\n\n## 当前状态\n已完成\n\n## 下一步\n跑测试\n\n## 相关文件\ninternal/parse.go"
	if missing := MissingCompactionSections(chinese); len(missing) != 0 {
		t.Fatalf("translated headings flagged: %#v", missing)
	}

	partial := "## Objective\nship it\n\n## Work State\ndone"
	if missing := MissingCompactionSections(partial); !slices.Equal(missing, []string{"Next Move", "Relevant Files"}) {
		t.Fatalf("partial summary reported wrong sections: %#v", missing)
	}

	// A prose summary without any anchored section is the case the note exists
	// for: it compacts fine, yet the next turn has no anchors at all.
	if missing := MissingCompactionSections("condensed history"); !slices.Equal(missing, []string{"Objective", "Work State", "Next Move", "Relevant Files"}) {
		t.Fatalf("unstructured summary reported wrong sections: %#v", missing)
	}
}

func TestCompactionInstructionsListTheAnchoredSections(t *testing.T) {
	for _, section := range compactionSections {
		if !strings.Contains(compactionInstructions, "## "+section) {
			t.Fatalf("compaction instructions omit the %q heading", section)
		}
	}
}
