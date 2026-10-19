package responses

import (
	"fmt"
	"strings"
	"unicode"
)

const (
	// Tool output is the most useful part of a compacted coding turn, so the
	// newest result gets a larger share of the tail than ordinary messages.
	maxCompactionLatestToolResultRunes = 12000
	maxCompactionSecondToolResultRunes = 6000
	maxCompactionOlderToolResultRunes  = 3000

	// Long command output is reduced in three passes: head, error excerpts and
	// tail. The markers keep the omission visible to the model.
	compactionMiddleMarker    = "\n… [middle omitted] …\n"
	compactionErrorSeparator  = "\n…\n"
	maxCompactionErrorRunes   = 2000
	compactionErrorContext    = 300
	minCompactionErrorExcerpt = 12
)

// compactionErrorMarkers are deliberately broad: a false positive only costs
// a little space, while a missed failure often forces the next model to rerun
// the command. Matching is case-insensitive and also covers common Chinese
// failure text.
var compactionErrorMarkers = [][]rune{
	[]rune("error"), []rune("fatal"), []rune("panic"), []rune("traceback"), []rune("exception"),
	[]rune("failed"), []rune("failure"), []rune("fail"), []rune("timeout"), []rune("timed out"),
	[]rune("permission denied"), []rune("access denied"), []rune("not found"), []rune("no such file"),
	[]rune("exit code"), []rune("exit status"), []rune("segmentation fault"), []rune("assertion failed"),
	[]rune("错误"), []rune("失败"), []rune("异常"), []rune("超时"), []rune("找不到"), []rune("拒绝"),
}

var compactionErrorMarkersByFirst = func() map[rune][][]rune {
	index := make(map[rune][][]rune)
	for _, marker := range compactionErrorMarkers {
		first := unicode.ToLower(marker[0])
		index[first] = append(index[first], marker)
	}
	return index
}()

type compactionTextRange struct {
	start  int
	end    int
	anchor int
}

// applyCompactionToolBudgets assigns more verbatim space to newer tool groups.
// A group can contain several parallel tool results; its budget is split
// between them so one large result cannot crowd out the rest of the turn.
func applyCompactionToolBudgets(groups []compactionGroup) {
	toolRank := 0
	for index := len(groups) - 1; index >= 0; index-- {
		resultIndexes := compactionToolResultIndexes(groups[index])
		if len(resultIndexes) == 0 {
			continue
		}
		budget := maxCompactionOlderToolResultRunes
		switch toolRank {
		case 0:
			budget = maxCompactionLatestToolResultRunes
		case 1:
			budget = maxCompactionSecondToolResultRunes
		}
		toolRank++

		parts := allocateCompactionToolBudgets(len(resultIndexes), budget)
		for part, messageIndex := range resultIndexes {
			label, body, ok := splitCompactionToolResult(groups[index].messages[messageIndex].Text)
			if !ok {
				continue
			}
			bodyBudget := parts[part] - len([]rune(label))
			if bodyBudget <= 0 {
				groups[index].messages[messageIndex].Text = label
				continue
			}
			groups[index].messages[messageIndex].Text = label + compactCleanedToolOutput(body, bodyBudget)
		}
	}
}

func compactionToolResultIndexes(group compactionGroup) []int {
	indexes := make([]int, 0, len(group.messages))
	for index, message := range group.messages {
		if message.toolResult {
			indexes = append(indexes, index)
		}
	}
	return indexes
}

func splitCompactionToolResult(text string) (label, body string, ok bool) {
	if !strings.HasPrefix(text, "[tool result") {
		return "", "", false
	}
	if end := strings.IndexByte(text, '\n'); end >= 0 {
		return text[:end+1], text[end+1:], true
	}
	return text, "", true
}

// isCompactionToolResultText recognizes the replay form produced by
// splitCompactionToolResult. Replayed compaction items have already lost the
// original role, but their exact label lets a later compaction keep applying
// the tool-output budget instead of truncating them as ordinary user text.
func isCompactionToolResultText(text string) bool {
	if !strings.HasPrefix(text, "[tool result") {
		return false
	}
	end := strings.IndexByte(text, '\n')
	if end < 0 || !strings.HasSuffix(text[:end], "]") {
		return false
	}
	return true
}

func allocateCompactionToolBudgets(count, total int) []int {
	budgets := make([]int, count)
	if count <= 0 || total <= 0 {
		return budgets
	}
	base := total / count
	remainder := total % count
	for index := range budgets {
		budgets[index] = base
	}
	// Extra runes go to the newest results in the group.
	for index := count - 1; index >= 0 && remainder > 0; index-- {
		budgets[index]++
		remainder--
	}
	return budgets
}

// compactToolOutput removes terminal noise before applying a bounded
// head/error/tail excerpt. It never returns more than limit runes.
func compactToolOutput(value string, limit int) string {
	return compactCleanedToolOutput(cleanCompactionToolOutput(value), limit)
}

func compactCleanedToolOutput(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}

	markerRunes := len([]rune(compactionMiddleMarker))
	if limit <= markerRunes*2+40 {
		return truncateMiddleRunes(runes, limit)
	}

	available := limit - markerRunes*2
	headBudget := available / 4
	tailBudget := available * 2 / 5
	errorBudget := available - headBudget - tailBudget
	if errorBudget < minCompactionErrorExcerpt {
		return truncateMiddleRunes(runes, limit)
	}

	headEnd := headBudget
	tailStart := len(runes) - tailBudget
	errorRanges, errorRunes := selectCompactionErrorRanges(runes, headEnd, tailStart, errorBudget)
	if len(errorRanges) == 0 {
		return truncateMiddleRunes(runes, limit)
	}
	// If the error excerpts are small, spend the rest of the budget on the
	// surrounding head and tail instead of leaving a large unclaimed gap.
	remaining := available - errorRunes
	headEnd = remaining / 3
	tailBudget = remaining - headEnd
	if headEnd > errorRanges[0].start {
		headEnd = errorRanges[0].start
	}
	if tailLimit := len(runes) - errorRanges[len(errorRanges)-1].end; tailBudget > tailLimit {
		tailBudget = tailLimit
	}

	var builder strings.Builder
	if headEnd > 0 {
		builder.WriteString(string(runes[:headEnd]))
		builder.WriteString(compactionMiddleMarker)
	}
	builder.WriteString(renderCompactionErrorRanges(runes, errorRanges))
	if tailBudget > 0 {
		builder.WriteString(compactionMiddleMarker)
		builder.WriteString(string(runes[len(runes)-tailBudget:]))
	}
	return builder.String()
}

// truncateMiddleRunes is the no-error fallback. It gives the tail more space
// because command failures and exit status usually appear at the end.
func truncateMiddleRunes(value []rune, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return string(value)
	}
	marker := []rune(compactionMiddleMarker)
	if limit <= len(marker) {
		return string(value[:limit])
	}
	available := limit - len(marker)
	head := available / 3
	tail := available - head
	return string(value[:head]) + compactionMiddleMarker + string(value[len(value)-tail:])
}

// cleanCompactionToolOutput makes terminal output deterministic: progress
// redraws keep their final frame, ANSI escapes disappear, consecutive
// duplicate lines collapse, and repeated blank lines become one.
func cleanCompactionToolOutput(value string) string {
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = stripTerminalEscapes(value)
	rawLines := strings.Split(value, "\n")
	lines := make([]string, 0, len(rawLines))
	pending := ""
	pendingCount := 0
	blank := false
	flushPending := func() {
		if pendingCount == 0 {
			return
		}
		keep := pendingCount
		if keep > 3 {
			keep = 3
		}
		for index := 0; index < keep; index++ {
			lines = append(lines, pending)
		}
		if omitted := pendingCount - keep; omitted > 0 {
			lines = append(lines, fmt.Sprintf("… [%d identical lines omitted] …", omitted))
		}
		pending = ""
		pendingCount = 0
	}
	for _, line := range rawLines {
		if index := strings.LastIndexByte(line, '\r'); index >= 0 {
			line = line[index+1:]
		}
		line = strings.TrimRight(line, " \t")
		if line == "" {
			flushPending()
			if len(lines) == 0 || blank {
				continue
			}
			blank = true
			lines = append(lines, "")
			continue
		}
		blank = false
		if pendingCount > 0 && line == pending {
			pendingCount++
			continue
		}
		flushPending()
		pending = line
		pendingCount = 1
	}
	flushPending()
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// stripTerminalEscapes removes CSI/OSC sequences while leaving UTF-8 bytes
// untouched. It intentionally ignores malformed escapes instead of dropping
// the rest of the output.
func stripTerminalEscapes(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for index := 0; index < len(value); {
		if value[index] != 0x1b {
			builder.WriteByte(value[index])
			index++
			continue
		}
		index++
		if index >= len(value) {
			break
		}
		switch value[index] {
		case '[':
			index++
			for index < len(value) {
				value := value[index]
				index++
				if value >= 0x40 && value <= 0x7e {
					break
				}
			}
		case ']':
			index++
			for index < len(value) {
				if value[index] == '\a' {
					index++
					break
				}
				if value[index] == 0x1b && index+1 < len(value) && value[index+1] == '\\' {
					index += 2
					break
				}
				index++
			}
		default:
			// Two-byte ESC sequence such as ESC(B.
			index++
		}
	}
	return builder.String()
}

func selectCompactionErrorRanges(text []rune, low, high, budget int) ([]compactionTextRange, int) {
	if budget <= 0 || high <= low {
		return nil, 0
	}
	ranges := findCompactionErrorRanges(text)
	separatorRunes := len([]rune(compactionErrorSeparator))
	selected := make([]compactionTextRange, 0, len(ranges))
	used := 0
	for index := len(ranges) - 1; index >= 0; index-- {
		current := clipCompactionTextRange(ranges[index], low, high)
		if current.end-current.start < minCompactionErrorExcerpt {
			continue
		}
		cost := current.end - current.start
		extra := 0
		if len(selected) > 0 {
			extra = separatorRunes
		}
		if used+cost+extra > budget {
			if len(selected) == 0 {
				if trimmed, ok := trimCompactionTextRange(current, budget); ok {
					selected = append(selected, trimmed)
					used = budget
				}
			}
			continue
		}
		selected = append(selected, current)
		used += cost + extra
	}
	if len(selected) == 0 {
		return nil, 0
	}
	for left, right := 0, len(selected)-1; left < right; left, right = left+1, right-1 {
		selected[left], selected[right] = selected[right], selected[left]
	}
	return selected, used
}

func renderCompactionErrorRanges(text []rune, ranges []compactionTextRange) string {
	var builder strings.Builder
	for index, current := range ranges {
		if index > 0 {
			builder.WriteString(compactionErrorSeparator)
		}
		builder.WriteString(string(text[current.start:current.end]))
	}
	return builder.String()
}

func findCompactionErrorRanges(text []rune) []compactionTextRange {
	ranges := make([]compactionTextRange, 0, 8)
	for index := 0; index < len(text); index++ {
		end, ok := compactionErrorMatchAt(text, index)
		if !ok {
			continue
		}
		current := expandCompactionErrorRange(text, index, end)
		if len(ranges) > 0 {
			last := &ranges[len(ranges)-1]
			if current.start <= last.end {
				if current.end > last.end {
					last.end = current.end
				}
				last.anchor = current.anchor
				index = end - 1
				continue
			}
		}
		ranges = append(ranges, current)
		index = end - 1
	}
	return ranges
}

func compactionErrorMatchAt(text []rune, index int) (int, bool) {
	for _, needle := range compactionErrorMarkersByFirst[unicode.ToLower(text[index])] {
		if index+len(needle) > len(text) {
			continue
		}
		matched := true
		for offset, expected := range needle {
			if unicode.ToLower(text[index+offset]) != unicode.ToLower(expected) {
				matched = false
				break
			}
		}
		if matched {
			return index + len(needle), true
		}
	}
	return 0, false
}

func expandCompactionErrorRange(text []rune, matchStart, matchEnd int) compactionTextRange {
	start := matchStart - compactionErrorContext
	if start < 0 {
		start = 0
	}
	for start > 0 && text[start-1] != '\n' && matchStart-start < maxCompactionErrorRunes/2 {
		start--
	}
	end := matchEnd + compactionErrorContext
	if end > len(text) {
		end = len(text)
	}
	for end < len(text) && text[end] != '\n' && end-matchEnd < maxCompactionErrorRunes/2 {
		end++
	}
	if end-start > maxCompactionErrorRunes {
		half := maxCompactionErrorRunes / 2
		start = matchStart - half
		if start < 0 {
			start = 0
		}
		end = start + maxCompactionErrorRunes
		if end > len(text) {
			end = len(text)
			start = end - maxCompactionErrorRunes
			if start < 0 {
				start = 0
			}
		}
	}
	return compactionTextRange{start: start, end: end, anchor: matchStart}
}

func clipCompactionTextRange(current compactionTextRange, low, high int) compactionTextRange {
	if current.end <= low || current.start >= high {
		return compactionTextRange{start: low, end: low, anchor: low}
	}
	if current.start < low {
		current.start = low
	}
	if current.end > high {
		current.end = high
	}
	if current.anchor < current.start {
		current.anchor = current.start
	}
	if current.anchor >= current.end {
		current.anchor = current.end - 1
	}
	return current
}

func trimCompactionTextRange(current compactionTextRange, budget int) (compactionTextRange, bool) {
	if budget <= 0 {
		return compactionTextRange{}, false
	}
	if current.end-current.start <= budget {
		return current, true
	}
	start := current.anchor - budget/2
	if start < current.start {
		start = current.start
	}
	end := start + budget
	if end > current.end {
		end = current.end
		start = end - budget
	}
	if start < current.start || end <= start {
		return compactionTextRange{}, false
	}
	current.start = start
	current.end = end
	return current, true
}
