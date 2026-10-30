package responses

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Hxjcc/cline-pass-switcher-go/internal/jsonx"
)

// The Chat backend has no native compaction endpoint. Explicitly ask for a
// handoff summary, and disable ordinary task execution for this generation.
const compactionInstructions = `Create a concise handoff summary of the preceding conversation for another model to continue from.
This is a compaction task. Do not answer the last user question, continue the original task, or call tools.
Return only the complete summary as visible plain text, using exactly these four sections and their headings:

## Objective
The user's goal, constraints and acceptance criteria.

## Work State
What is done, what was verified, what failed and which decisions were taken. Keep exact paths, commands, identifiers and error text.

## Next Move
The single next action to take, plus pending work and open questions.

## Relevant Files
Files, directories, endpoints or config keys that matter, one line each.

Keep every fact another model needs to continue accurately; drop small talk. For images, describe the visually relevant facts needed for the task, rather than only stating that an image was attached. Thinking alone is not a summary.`

const (
	// maxCompactionRecentMessageRunes caps one ordinary user/assistant message.
	// Tool results use the recency-aware budgets in compaction_output.go.
	maxCompactionRecentMessageRunes = 2000
	// maxCompactionRecentTotalRunes caps the whole verbatim tail so the
	// envelope stays small enough to send back on every request.
	maxCompactionRecentTotalRunes = 32000
	// compactionEscalatedFloorTokens is what a retry starts from before the
	// doubling below; the exported ceiling keeps one retry inside the model's
	// window and is reported by the console's effective-settings view.
	compactionEscalatedFloorTokens   = 16384
	CompactionEscalatedCeilingTokens = 32768
)

// compactionSections are the anchored headings compaction summaries must
// carry. A summary that ends normally but drops one of them still compacts
// fine, yet the next turn loses the "what next" or "which files" anchor, so
// callers record the omission in the history instead of retrying.
var compactionSections = []string{"Objective", "Work State", "Next Move", "Relevant Files"}

// compactionSectionAliases widens each heading with the spellings models
// actually emit. The check is advisory, so a generous alias list only ever
// costs a missed note - never a wasted retry.
var compactionSectionAliases = map[string][]string{
	"Objective":      {"objective", "goal", "目标"},
	"Work State":     {"workstate", "state", "当前状态", "工作状态"},
	"Next Move":      {"nextmove", "nextstep", "下一步"},
	"Relevant Files": {"relevantfiles", "files", "paths", "相关文件", "涉及文件"},
}

// MissingCompactionSections reports the anchored sections a summary omitted,
// in template order. Matching is deliberately loose - case, markup and
// separators are ignored - because the result feeds an advisory history note
// rather than a retry: a false positive would nag about a fine summary.
func MissingCompactionSections(summary string) []string {
	haystack := normalizeSectionText(summary)
	missing := make([]string, 0, len(compactionSections))
	for _, section := range compactionSections {
		if !sectionMentioned(haystack, section) {
			missing = append(missing, section)
		}
	}
	return missing
}

func sectionMentioned(haystack, section string) bool {
	for _, alias := range compactionSectionAliases[section] {
		if strings.Contains(haystack, normalizeSectionText(alias)) {
			return true
		}
	}
	return false
}

// normalizeSectionText folds the decorations a model may wrap a heading in.
func normalizeSectionText(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for _, char := range strings.ToLower(value) {
		switch char {
		case ' ', '\t', '\n', '\r', '-', '_', '*', '#', ':', '.', '`', '\'', '"', '/', '\\':
			continue
		}
		builder.WriteRune(char)
	}
	return builder.String()
}

// CompactionRecentMessage is one verbatim turn kept inside a compaction item.
type CompactionRecentMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
	// SourceHash identifies the full text when Text is only an excerpt. It
	// keeps distinct long requests with the same prefix distinguishable.
	SourceHash string `json:"source_hash,omitempty"`
	// toolResult is set only while building a fresh tail. It is deliberately
	// unexported so envelopes stay compatible with older payloads.
	toolResult bool
}

// CompactionPayload is what the ocx1: envelope carries. Older envelopes hold
// the summary text directly; decoding still accepts those.
type CompactionPayload struct {
	Summary         string                    `json:"summary"`
	Recent          []CompactionRecentMessage `json:"recent,omitempty"`
	Degraded        bool                      `json:"degraded,omitempty"`
	PreviousSummary string                    `json:"previous_summary,omitempty"`
}

func ToCompactionChatWithOptions(body map[string]any, options Options) (map[string]any, *Context, error) {
	chat, context, err := ToChatWithOptions(body, options)
	if err != nil {
		return nil, nil, err
	}
	context.compactionUsers = compactionUserMessages(body["input"])
	messages := jsonx.Slice(chat["messages"])
	leading := 0
	for leading < len(messages) && jsonx.Map(messages[leading])["role"] == "system" {
		// A previous compaction summary is source history, not a new system
		// instruction. Move it into the history to be summarized again instead
		// of keeping it outside and adding another summary beside it.
		if strings.HasPrefix(jsonx.String(jsonx.Map(messages[leading])["content"]), "Earlier conversation was compacted. Summary:") {
			break
		}
		leading++
	}
	// Keep the newest turns verbatim and summarize only what came before them:
	// duplicating the same turns in both places would waste context and confuse
	// the next model.
	history := messages[leading:]
	tail, cut := selectCompactionTailWithCut(history, options.RecentCompactionTokens)
	tailSelected := len(tail) > 0 && cut > 0
	if cut < 0 || cut > len(history) {
		cut = len(history)
	}
	summaryEnd := cut
	if cut <= 0 {
		// The tail covered the whole conversation. Keep it for exact replay and
		// degraded fallback, but summarize the full history so a short session
		// does not produce an empty or misleading summary.
		summaryEnd = len(history)
	}
	// Text excerpts cannot replace the rest of a long message or its images.
	// Include those original turns in the summary even when their excerpts
	// also belong to the retained tail.
	summaryEnd = compactionSummaryBoundary(history, summaryEnd)
	context.compactionRecent = tail

	summaryInstructions := compactionInstructions
	var toolTranscript string
	if tailSelected {
		if transcript := compactionToolTranscript(history[summaryEnd:]); transcript != "" {
			toolTranscript = transcript
		}
	}
	summaryInstructions += "\n\n" + untrustedHistoryPolicy
	prepared := make([]any, 0, len(messages)+2)
	prepared = append(prepared, messages[:leading]...)
	prepared = append(prepared, map[string]any{"role": "system", "content": summaryInstructions})
	prepared = append(prepared, history[:summaryEnd]...)
	if toolTranscript != "" {
		prepared = append(prepared, untrustedToolTranscript(toolTranscript))
	}
	prepared = append(prepared, map[string]any{"role": "user", "content": compactionInstructions})
	chat["messages"] = prepared
	// Compaction inherits the session's maximum reasoning by default: a capped
	// level tends to spend the whole output budget on hidden thinking and the
	// gateway then answers "empty response content", which costs a wasted pass
	// plus an escalated retry. The caller still escalates once if it happens.
	if effort := compactionReasoningEffort(options.ReasoningEfforts, options.CompactionReasoningEffort); effort != "" {
		chat["reasoning_effort"] = effort
		// The history and response headers should show the level compaction
		// actually ran at, while RequestedReasoningEffort keeps the session's
		// original choice.
		context.MappedReasoningEffort = effort
		if reasoningEffortOff(effort) {
			delete(chat, "reasoning")
		} else {
			chat["reasoning"] = map[string]any{"effort": effort}
		}
	}
	for _, key := range []string{"stream", "stream_options", "tools", "tool_choice", "parallel_tool_calls", "response_format"} {
		delete(chat, key)
	}
	context.ResponseTools = []any{}
	context.ResponseToolChoice = "none"
	context.ResponseText = map[string]any{"format": map[string]any{"type": "text"}}
	context.outputSchema = nil
	context.ParallelToolCalls = false
	return chat, context, nil
}

func compactionSummaryBoundary(messages []any, cut int) int {
	end := cut
	for index := cut; index < len(messages); index++ {
		message := jsonx.Map(messages[index])
		role := jsonx.String(message["role"])
		needsSummary := false
		if role == "user" || role == "assistant" {
			needsSummary = utf8.RuneCountInString(collectPartText(message["content"])) > maxCompactionRecentMessageRunes
			for _, raw := range jsonx.Slice(message["content"]) {
				if kind := jsonx.String(jsonx.Map(raw)["type"]); kind != "" && kind != "text" {
					needsSummary = true
				}
			}
			for _, raw := range jsonx.Slice(message["tool_calls"]) {
				function := jsonx.Map(jsonx.Map(raw)["function"])
				if utf8.RuneCountInString(jsonx.String(function["arguments"])) > 500 {
					needsSummary = true
				}
			}
		}
		if needsSummary {
			end = index + 1
			// Never cut between an assistant's calls and their tool results.
			for end < len(messages) && jsonx.String(jsonx.Map(messages[end])["role"]) == "tool" {
				end++
			}
		}
	}
	return end
}

// compactionGroup keeps an assistant tool call together with its tool results
// so the tail boundary never splits a tool-call group. The replay form is text:
// a tool result replayed as a bare Chat tool message would be rejected when the
// matching call is not part of the same request.
type compactionGroup struct {
	start    int
	messages []CompactionRecentMessage
}

func buildCompactionGroups(messages []any) []compactionGroup {
	groups := make([]compactionGroup, 0, len(messages))
	for index := 0; index < len(messages); {
		message := jsonx.Map(messages[index])
		if message == nil {
			index++
			continue
		}
		role := jsonx.String(message["role"])
		if role == "assistant" {
			text := strings.TrimSpace(collectPartText(message["content"]))
			calls := jsonx.Slice(message["tool_calls"])
			if len(calls) > 0 {
				parts := make([]string, 0, len(calls)+1)
				if text != "" {
					parts = append(parts, text)
				}
				for _, rawCall := range calls {
					call := jsonx.Map(rawCall)
					function := jsonx.Map(call["function"])
					name := jsonx.String(function["name"])
					if name == "" {
						name = "unknown"
					}
					arguments := jsonx.String(function["arguments"])
					if arguments == "" {
						arguments = "{}"
					}
					parts = append(parts, "[tool call "+name+"] "+truncateText(arguments, 500))
				}
				recent := make([]CompactionRecentMessage, 0, 1)
				if len(parts) > 0 {
					recent = append(recent, CompactionRecentMessage{Role: "assistant", Text: strings.Join(parts, "\n")})
				}
				next := index + 1
				for next < len(messages) {
					tool := jsonx.Map(messages[next])
					if jsonx.String(tool["role"]) != "tool" {
						break
					}
					callID := strings.TrimSpace(jsonx.String(tool["tool_call_id"]))
					toolText := strings.TrimSpace(collectPartText(tool["content"]))
					if toolText == "" {
						toolText = "(no output)"
					}
					label := "[tool result"
					if callID != "" {
						label += " " + callID
					}
					label += "]\n"
					recent = append(recent, CompactionRecentMessage{
						Role: "user", Text: label + cleanCompactionToolOutput(toolText), toolResult: true,
					})
					next++
				}
				if len(recent) > 0 {
					groups = append(groups, compactionGroup{start: index, messages: recent})
				}
				index = next
				continue
			}
			if text != "" {
				groups = append(groups, compactionGroup{
					start: index, messages: []CompactionRecentMessage{compactionRecentText("assistant", text)},
				})
			}
			index++
			continue
		}
		if role == "user" {
			if text := strings.TrimSpace(collectPartText(message["content"])); text != "" {
				if isCompactionToolResultText(text) {
					groups = append(groups, compactionGroup{
						start: index,
						messages: []CompactionRecentMessage{{
							Role: "user", Text: cleanCompactionToolOutput(text), toolResult: true,
						}},
					})
					index++
					continue
				}
				groups = append(groups, compactionGroup{
					start: index, messages: []CompactionRecentMessage{compactionRecentText("user", text)},
				})
			}
			index++
			continue
		}
		if role == "tool" {
			callID := strings.TrimSpace(jsonx.String(message["tool_call_id"]))
			toolText := strings.TrimSpace(collectPartText(message["content"]))
			if toolText == "" {
				toolText = "(no output)"
			}
			label := "[tool result"
			if callID != "" {
				label += " " + callID
			}
			label += "]\n"
			groups = append(groups, compactionGroup{
				start: index,
				messages: []CompactionRecentMessage{{
					Role: "user", Text: label + cleanCompactionToolOutput(toolText), toolResult: true,
				}},
			})
		}
		index++
	}
	applyCompactionToolBudgets(groups)
	return groups
}

func compactionGroupUsage(group compactionGroup) (tokens, runes int) {
	for _, message := range group.messages {
		tokens += estimateCompactionTokens(message.Text)
		runes += len([]rune(message.Text))
	}
	return tokens, runes
}

func selectCompactionTailWithCut(messages []any, budgetTokens int) ([]CompactionRecentMessage, int) {
	if budgetTokens <= 0 || len(messages) == 0 {
		return nil, len(messages)
	}
	groups := buildCompactionGroups(messages)
	if len(groups) == 0 {
		return nil, len(messages)
	}
	selected := make([]compactionGroup, 0, len(groups))
	tokens, runes := 0, 0
	for index := len(groups) - 1; index >= 0; index-- {
		group := groups[index]
		groupTokens, groupRunes := compactionGroupUsage(group)
		if len(selected) > 0 && (tokens+groupTokens > budgetTokens || runes+groupRunes > maxCompactionRecentTotalRunes) {
			break
		}
		if len(selected) == 0 && groupRunes > maxCompactionRecentTotalRunes {
			break
		}
		selected = append(selected, group)
		tokens += groupTokens
		runes += groupRunes
	}
	if len(selected) == 0 {
		return nil, len(messages)
	}
	slices.Reverse(selected)
	// Keep the oldest-first order and, when there is more than one group,
	// start at a user turn so replay never begins with an answer to a dropped
	// question. A single assistant answer is kept because it may be the only
	// final output of the compacted turn.
	for len(selected) > 1 && selected[0].messages[0].Role != "user" && !compactionGroupHasToolResult(selected[0]) {
		selected = selected[1:]
	}
	recent := make([]CompactionRecentMessage, 0, len(messages))
	for _, group := range selected {
		recent = append(recent, group.messages...)
	}
	return recent, selected[0].start
}

func compactionGroupHasToolResult(group compactionGroup) bool {
	for _, message := range group.messages {
		if message.toolResult {
			return true
		}
	}
	return false
}

// selectCompactionTail is the compatibility wrapper used by tests and callers
// that only need the replay text.
func selectCompactionTail(messages []any, budgetTokens int) []CompactionRecentMessage {
	tail, _ := selectCompactionTailWithCut(messages, budgetTokens)
	return tail
}

// compactionToolTranscript gives the summarizer a compact, exact view of tool
// calls and results. Those messages may be excluded from the summary prompt
// because they belong to the verbatim tail; without this transcript a summary
// could claim that a command was never executed.
func compactionToolTranscript(messages []any) string {
	const (
		maxLineRunes  = 600
		maxTotalRunes = 6000
	)
	lines := make([]string, 0, 8)
	total := 0
	add := func(line string) {
		line = compactCleanedToolOutput(line, maxLineRunes)
		lines = append(lines, line)
		total += len([]rune(line)) + 1
		for total > maxTotalRunes && len(lines) > 0 {
			total -= len([]rune(lines[0])) + 1
			lines[0] = ""
			lines = lines[1:]
		}
	}
	for _, raw := range messages {
		message := jsonx.Map(raw)
		if message == nil {
			continue
		}
		switch jsonx.String(message["role"]) {
		case "assistant":
			for _, rawCall := range jsonx.Slice(message["tool_calls"]) {
				call := jsonx.Map(rawCall)
				function := jsonx.Map(call["function"])
				name := jsonx.String(function["name"])
				if name == "" {
					name = "unknown"
				}
				arguments := jsonx.String(function["arguments"])
				if arguments == "" {
					arguments = "{}"
				}
				line := "- assistant called " + name + " with arguments " + truncateText(arguments, maxLineRunes)
				add(line)
			}
		case "tool":
			callID := strings.TrimSpace(jsonx.String(message["tool_call_id"]))
			text := strings.TrimSpace(collectPartText(message["content"]))
			if text == "" {
				text = "(no output)"
			}
			label := "- tool result"
			if callID != "" {
				label += " (" + callID + ")"
			}
			line := label + ": " + compactToolOutput(text, maxLineRunes)
			add(line)
		}
	}
	return strings.Join(lines, "\n")
}

func compactionSourceHash(text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return hex.EncodeToString(sum[:])
}

func compactionRecentText(role, text string) CompactionRecentMessage {
	message := CompactionRecentMessage{Role: role, Text: truncateText(text, maxCompactionRecentMessageRunes)}
	if message.Text != text {
		message.SourceHash = compactionSourceHash(text)
	}
	return message
}

// mergeCompactionRecent restores an ordered tail among the messages retained
// before its compaction item. Matching occurrences are anchors, not a set of
// text to delete: a repeated user request must keep its corresponding answer.
// Reusing the original anchor also preserves images and other content parts.
func mergeCompactionRecent(messages []any, recent []CompactionRecentMessage) []any {
	type messageKey struct{ role, text string }
	tail := make([]CompactionRecentMessage, 0, len(recent))
	for _, message := range recent {
		message.Role = strings.TrimSpace(message.Role)
		message.Text = strings.TrimSpace(message.Text)
		if (message.Role == "user" || message.Role == "assistant") && message.Text != "" {
			tail = append(tail, message)
		}
	}
	if len(tail) == 0 {
		return messages
	}
	positions := make(map[messageKey][]int, len(messages))
	sources := make(map[messageKey][]int)
	legacyExcerpts := make(map[messageKey][]int)
	for index, raw := range messages {
		message := jsonx.Map(raw)
		key := messageKey{jsonx.String(message["role"]), strings.TrimSpace(collectPartText(message["content"]))}
		positions[key] = append(positions[key], index)
		if utf8.RuneCountInString(key.text) > maxCompactionRecentMessageRunes {
			source := messageKey{key.role, compactionSourceHash(key.text)}
			sources[source] = append(sources[source], index)
			excerpt := messageKey{key.role, truncateText(key.text, maxCompactionRecentMessageRunes)}
			legacyExcerpts[excerpt] = append(legacyExcerpts[excerpt], index)
		}
	}
	// Match backwards because recent is the newest part of the conversation.
	// Each occurrence can anchor once, and anchor order must remain monotonic.
	anchors := make([]int, len(tail))
	before := len(messages)
	for index := len(tail) - 1; index >= 0; index-- {
		anchors[index] = -1
		matches := positions[messageKey{tail[index].Role, tail[index].Text}]
		if tail[index].SourceHash != "" {
			matches = sources[messageKey{tail[index].Role, tail[index].SourceHash}]
		} else if len(matches) == 0 {
			// Older envelopes carry no source hash. Only a unique full-text
			// candidate can safely identify a truncated legacy excerpt.
			candidates := legacyExcerpts[messageKey{tail[index].Role, tail[index].Text}]
			if len(candidates) > 0 {
				first := jsonx.Map(messages[candidates[0]])
				text := strings.TrimSpace(collectPartText(first["content"]))
				unique := true
				for _, candidate := range candidates[1:] {
					if strings.TrimSpace(collectPartText(jsonx.Map(messages[candidate])["content"])) != text {
						unique = false
						break
					}
				}
				if unique {
					matches = candidates
				}
			}
		}
		if match := sort.SearchInts(matches, before) - 1; match >= 0 {
			anchors[index] = matches[match]
			before = matches[match]
		}
	}
	merged := make([]any, 0, len(messages)+len(tail))
	appendTail := func(start, end int) {
		for _, message := range tail[start:end] {
			merged = append(merged, map[string]any{"role": message.Role, "content": message.Text})
		}
	}
	cursor, pending := 0, 0
	for index, anchor := range anchors {
		if anchor < 0 {
			continue
		}
		merged = append(merged, messages[cursor:anchor]...)
		appendTail(pending, index)
		merged = append(merged, messages[anchor])
		cursor, pending = anchor+1, index+1
	}
	if pending == 0 {
		// An envelope without any shared messages follows the retained history.
		merged = append(merged, messages...)
		appendTail(0, len(tail))
		return merged
	}
	appendTail(pending, len(tail))
	return append(merged, messages[cursor:]...)
}

// estimateCompactionTokens approximates the tokenizer closely enough to bound
// the verbatim tail: CJK runes are about one token, other text about a quarter.
func estimateCompactionTokens(text string) int {
	cjk, other := 0, 0
	for _, r := range text {
		if r >= 0x2E80 {
			cjk++
		} else {
			other++
		}
	}
	return cjk + other/4 + 1
}

// compactionReasoningEffort resolves the level compaction runs at. The
// configured preference wins when the model advertises it; "auto" (or an
// unknown level) falls back to the strongest advertised level, and only a
// model without a real thinking level ends up at "none".
func compactionReasoningEffort(efforts []string, configured string) string {
	configured = strings.ToLower(strings.TrimSpace(configured))
	if configured != "" && configured != "auto" {
		for _, effort := range efforts {
			if strings.EqualFold(strings.TrimSpace(effort), configured) {
				return configured
			}
		}
	}
	for _, preferred := range []string{"max", "xhigh", "high", "medium", "low", "minimal"} {
		for _, effort := range efforts {
			if strings.EqualFold(strings.TrimSpace(effort), preferred) {
				return preferred
			}
		}
	}
	// No graded level advertised: prefer any real thinking level over "none"
	// (a none/max toggle resolves to max), and fall back to disabling thinking.
	for _, effort := range efforts {
		if value := strings.TrimSpace(effort); value != "" && !reasoningEffortOff(value) {
			return value
		}
	}
	for _, effort := range efforts {
		if value := strings.TrimSpace(effort); value != "" {
			return value
		}
	}
	return ""
}

func reasoningEffortOff(effort string) bool {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "none", "off", "disabled":
		return true
	default:
		return false
	}
}

// EscalateCompactionBudget rewrites a compaction chat request for the retry
// pass: the model's highest advertised reasoning level plus a doubled output
// budget, so a summary that starved on hidden thinking gets room to land.
func EscalateCompactionBudget(body map[string]any, efforts []string) {
	if body == nil {
		return
	}
	if effort := highestReasoningEffort(efforts); effort != "" {
		body["reasoning_effort"] = effort
		if reasoningEffortOff(effort) {
			delete(body, "reasoning")
		} else {
			body["reasoning"] = map[string]any{"effort": effort}
		}
	}
	tokens := 0
	switch typed := body["max_tokens"].(type) {
	case float64:
		tokens = int(typed)
	case int:
		tokens = typed
	case int64:
		tokens = int(typed)
	case json.Number:
		value, _ := typed.Int64()
		tokens = int(value)
	}
	// The retry is the last pass before degradation, so it starts from the same
	// two-step floor the first pass uses and doubles onto the ceiling.
	if tokens < compactionEscalatedFloorTokens {
		tokens = compactionEscalatedFloorTokens
	}
	tokens *= 2
	if tokens > CompactionEscalatedCeilingTokens {
		tokens = CompactionEscalatedCeilingTokens
	}
	body["max_tokens"] = tokens
}

func highestReasoningEffort(efforts []string) string {
	for _, preferred := range []string{"max", "xhigh", "high", "medium", "low"} {
		for _, effort := range efforts {
			if strings.EqualFold(strings.TrimSpace(effort), preferred) {
				return preferred
			}
		}
	}
	return compactionReasoningEffort(efforts, "auto")
}

// Keep original user turns, including images, outside the summary. Construct
// DegradedCompactionResponse is the standalone-endpoint fallback used when no
// summary could be produced: the client still receives a valid compaction
// object, so the session keeps moving, at the cost of the older context.
func DegradedCompactionResponse(context *Context, reason, partial string) map[string]any {
	users := context.compactionUsers
	summaryContext := *context
	summaryContext.compactionUsers = nil
	output := append([]any(nil), users...)
	output = append(output, degradedCompactionItem(&summaryContext, reason, partial))
	return map[string]any{
		"id": newID("resp"), "object": "response.compaction", "created_at": time.Now().Unix(),
		"output": output, "usage": nil,
	}
}

// DegradedCompactionTriggerResponse is the remote-compaction v2 fallback: one
// normal response whose single output item is the degraded compaction item.
func DegradedCompactionTriggerResponse(context *Context, reason, partial string) map[string]any {
	return context.responseBase(
		newID("resp"), time.Now().Unix(), context.Model, "completed",
		[]any{degradedCompactionItem(context, reason, partial)}, nil, nil, "",
	)
}

func degradedCompactionItem(context *Context, reason, partial string) map[string]any {
	return map[string]any{
		"id":   newID("cmp"),
		"type": "compaction",
		"encrypted_content": encodeCompactionPayload(CompactionPayload{
			Summary:         degradedCompactionSummary(context, reason, partial),
			Recent:          context.compactionRecent,
			Degraded:        true,
			PreviousSummary: context.compactionPreviousSummary,
		}),
	}
}

// PartialCompactionSummary returns whatever visible text a failed summary
// attempt produced, so a degraded item can keep the usable part.
func PartialCompactionSummary(chat map[string]any) string {
	choices := jsonx.Slice(chat["choices"])
	if len(choices) == 0 {
		// Error bodies (500/503, rate limits) carry no completion to salvage.
		return ""
	}
	choice := jsonx.Map(choices[0])
	return strings.TrimSpace(collectPartText(jsonx.Map(choice["message"])["content"]))
}

// degradedCompactionSummary preserves the last successful summary when one
// exists. Without it, only the retained recent messages and partial new
// summary remain available.
func degradedCompactionSummary(context *Context, reason, partial string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "summary generation failed"
	}
	recent := make([]string, 0, 4)
	// The verbatim tail travels in the envelope's recent list; only list the
	// user requests inside the text when no structured tail was selected.
	if len(context.compactionRecent) == 0 {
		for _, raw := range context.compactionUsers {
			message := jsonx.Map(raw)
			text := strings.TrimSpace(collectPartText(message["content"]))
			if text == "" {
				text = strings.TrimSpace(jsonx.String(message["text"]))
			}
			if text == "" {
				continue
			}
			recent = append(recent, truncateText(text, 400))
		}
		if len(recent) > 3 {
			recent = recent[len(recent)-3:]
		}
	}
	var builder strings.Builder
	builder.WriteString("[compaction degraded] The earlier conversation could not be summarized: ")
	builder.WriteString(truncateText(reason, 200))
	builder.WriteString("\n\n## Objective\n")
	previous := strings.TrimSpace(context.compactionPreviousSummary)
	if previous != "" {
		builder.WriteString("Continue the objective in the last successful summary below, subject to newer user requests.\n")
	} else if len(recent) > 0 {
		builder.WriteString("Unknown beyond the recent user requests preserved below.\n")
	} else {
		builder.WriteString("Unknown: the earlier conversation is no longer available.\n")
	}
	builder.WriteString("\n## Work State\n")
	if previous != "" {
		builder.WriteString("The last successful summary is preserved. Progress after it may be missing except for the recent messages.\n\n<last_successful_summary>\n")
		builder.WriteString(previous)
		builder.WriteString("\n</last_successful_summary>\n")
	} else {
		builder.WriteString("Unavailable: summarization failed, so completed work and tool results from earlier turns were dropped.\n")
	}
	if partial = strings.TrimSpace(partial); partial != "" {
		builder.WriteString("\nPartial summary produced before the failure (may be cut off):\n")
		builder.WriteString(partial)
		builder.WriteString("\n")
	}
	if previous != "" {
		builder.WriteString("\n## Next Move\nUse the last successful summary together with newer retained messages, verify intervening progress, and continue the latest request.\n")
		builder.WriteString("\n## Relevant Files\nRecover paths from the last successful summary and the recent messages.\n")
	} else {
		builder.WriteString("\n## Next Move\nRe-read the recent user requests below and continue from the last one; ask the user to restate the goal if it is unclear.\n")
		builder.WriteString("\n## Relevant Files\nUnknown: recover paths from the recent requests below.\n")
	}
	if len(recent) > 0 {
		builder.WriteString("\n---\nRecent user requests (verbatim, oldest first):\n")
		for _, text := range recent {
			builder.WriteString("- ")
			builder.WriteString(strings.Join(strings.Fields(text), " "))
			builder.WriteString("\n")
		}
	}
	return strings.TrimSpace(builder.String())
}

func truncateText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

// Keep original user turns, including images, outside the summary. Construct
// output message objects without changing the request maps or content blocks.
func compactionUserMessages(input any) []any {
	items := jsonx.Slice(input)
	if text, ok := input.(string); ok {
		items = []any{text}
	}
	users := make([]any, 0)
	for _, raw := range items {
		var source map[string]any
		if text, ok := raw.(string); ok {
			source = map[string]any{"role": "user", "content": text}
		} else {
			source = jsonx.Map(raw)
		}
		if jsonx.String(source["role"]) != "user" {
			continue
		}
		message := make(map[string]any, len(source)+3)
		for key, value := range source {
			message[key] = value
		}
		message["type"], message["status"] = "message", "completed"
		if jsonx.String(message["id"]) == "" {
			message["id"] = newID("msg")
		}
		if text, ok := message["content"].(string); ok {
			message["content"] = []any{map[string]any{"type": "input_text", "text": text}}
		}
		users = append(users, message)
	}
	return users
}

const compactionEnvelopePrefix = "ocx1:"

// CompactionEvents preserves the proxy's streaming extension. The buffered
// endpoint returns response.compaction; SSE uses the normal Response lifecycle
// with retained user messages followed by the compaction item.
func CompactionEvents(compaction map[string]any, context *Context) []Event {
	response := context.responseBase(jsonx.String(compaction["id"]), intValue(compaction["created_at"]),
		context.Model, "completed", jsonx.Slice(compaction["output"]), compaction["usage"], nil, "")
	inProgress := context.responseBase(jsonx.String(compaction["id"]), intValue(compaction["created_at"]),
		context.Model, "in_progress", []any{}, nil, nil, "")
	events := []Event{
		event("response.created", map[string]any{"response": inProgress}),
		event("response.in_progress", map[string]any{"response": inProgress}),
	}
	for index, item := range jsonx.Slice(compaction["output"]) {
		events = append(events,
			event("response.output_item.added", map[string]any{"output_index": index, "item": item}),
			event("response.output_item.done", map[string]any{"output_index": index, "item": item}),
		)
	}
	return append(events, event("response.completed", map[string]any{"response": response}))
}

// CompactionEnvelope wraps a readable summary in the same opaque envelope shape
// used by Codex-compatible third-party compaction implementations. Envelopes
// with a verbatim tail (or a degraded marker) use the structured v2 encoding.
func CompactionEnvelope(summary string) string {
	return encodeCompactionPayload(CompactionPayload{Summary: summary})
}

func encodeCompactionPayload(payload CompactionPayload) string {
	if len(payload.Recent) == 0 && !payload.Degraded && strings.TrimSpace(payload.Summary) == payload.Summary && !strings.HasPrefix(payload.Summary, "{") {
		// Keep the historical shape for plain summaries: other tooling that
		// decodes the envelope as base64 text keeps working.
		return compactionEnvelopePrefix + base64.StdEncoding.EncodeToString([]byte(payload.Summary))
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return compactionEnvelopePrefix + base64.StdEncoding.EncodeToString([]byte(payload.Summary))
	}
	return compactionEnvelopePrefix + base64.StdEncoding.EncodeToString(raw)
}

// compactionPayloadFromEnvelope accepts both encodings: the structured v2 JSON
// and the historical plain-text summary.
func compactionPayloadFromEnvelope(value string) (CompactionPayload, bool) {
	encoded, found := strings.CutPrefix(value, compactionEnvelopePrefix)
	if !found {
		return CompactionPayload{}, false
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return CompactionPayload{}, false
	}
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "{") {
		var payload CompactionPayload
		if err := json.Unmarshal(raw, &payload); err != nil {
			return CompactionPayload{}, false
		}
		if payload.Summary == "" && len(payload.Recent) == 0 {
			return CompactionPayload{}, false
		}
		return payload, true
	}
	return CompactionPayload{Summary: trimmed}, trimmed != ""
}

func compactionSummaryFromEnvelope(value string) (string, bool) {
	payload, ok := compactionPayloadFromEnvelope(value)
	return payload.Summary, ok
}

// DecodeCompactionEnvelope reads a compaction envelope produced by this proxy
// (or by an older build that stored the summary as plain text).
func DecodeCompactionEnvelope(value string) (CompactionPayload, bool) {
	return compactionPayloadFromEnvelope(value)
}

// CompactionResponse converts a completed Chat Completions response into the
// Responses compaction shape expected by Codex. The readable summary is wrapped
// in a local envelope so later requests can restore it for third-party models.
func CompactionResponse(chat map[string]any, context *Context) (map[string]any, error) {
	response, err := FromChat(chat, context)
	if err != nil {
		return nil, err
	}
	summary, err := compactionSummary(chat, response)
	if err != nil {
		return nil, err
	}
	output := append([]any(nil), context.compactionUsers...)
	output = append(output, map[string]any{
		"id":   newID("cmp"),
		"type": "compaction",
		// Keep the complete order in the envelope; replay merges duplicate
		// users with their original messages, including images.
		"encrypted_content": encodeCompactionPayload(CompactionPayload{Summary: summary, Recent: context.compactionRecent}),
	})
	return map[string]any{
		"id": response["id"], "object": "response.compaction", "created_at": response["created_at"],
		"output": output, "usage": response["usage"],
	}, nil
}

// RequestTriggersCompaction reports whether the client asked for remote
// compaction v2. Codex appends a {type:"compaction_trigger"} input item to an
// otherwise ordinary Responses request and expects a single compaction item
// back over the normal streaming lifecycle.
func RequestTriggersCompaction(body map[string]any) bool {
	for _, raw := range jsonx.Slice(body["input"]) {
		item := jsonx.Map(raw)
		if item != nil && jsonx.String(item["type"]) == "compaction_trigger" {
			return true
		}
	}
	return false
}

// CompactionTriggerResponse builds the remote compaction v2 reply: one normal
// Responses object whose output holds exactly one compaction item. Codex counts
// response.output_item.done events, rejects anything but a single compaction
// item, and waits for response.completed.
func CompactionTriggerResponse(chat map[string]any, context *Context) (map[string]any, error) {
	response, err := FromChat(chat, context)
	if err != nil {
		return nil, err
	}
	summary, err := compactionSummary(chat, response)
	if err != nil {
		return nil, err
	}
	item := map[string]any{
		"id":                newID("cmp"),
		"type":              "compaction",
		"encrypted_content": encodeCompactionPayload(CompactionPayload{Summary: summary, Recent: context.compactionRecent}),
	}
	return context.responseBase(
		jsonx.String(response["id"]), intValue(response["created_at"]), context.Model, "completed",
		[]any{item}, response["usage"], nil, "",
	), nil
}

// CompactionTriggerEvents opens the standard Responses lifecycle around the
// remote compaction v2 reply.
func CompactionTriggerEvents(response map[string]any, context *Context) []Event {
	inProgress := context.responseBase(
		jsonx.String(response["id"]), intValue(response["created_at"]), context.Model, "in_progress",
		[]any{}, nil, nil, "",
	)
	events := []Event{
		event("response.created", map[string]any{"response": inProgress}),
		event("response.in_progress", map[string]any{"response": inProgress}),
	}
	for index, item := range jsonx.Slice(response["output"]) {
		events = append(events,
			event("response.output_item.added", map[string]any{"output_index": index, "item": item}),
			event("response.output_item.done", map[string]any{"output_index": index, "item": item}),
		)
	}
	return append(events, event("response.completed", map[string]any{"response": response}))
}

// compactionSummary validates a completed summarization turn and returns the
// readable summary both compaction shapes are built from.
func compactionSummary(chat, response map[string]any) (string, error) {
	if response["status"] != "completed" {
		reason := jsonx.String(jsonx.Map(response["incomplete_details"])["reason"])
		return "", &ChatFailure{Code: "compaction_incomplete", Type: "upstream_error",
			Message: "upstream compaction summary is incomplete: " + reason, Response: response}
	}
	choice := jsonx.Map(jsonx.Slice(chat["choices"])[0])
	message := jsonx.Map(choice["message"])
	if jsonx.String(choice["finish_reason"]) != "stop" || len(jsonx.Slice(message["tool_calls"])) > 0 {
		return "", errors.New("upstream compaction did not finish with a summary")
	}
	if jsonx.String(message["refusal"]) != "" {
		return "", errors.New("upstream refused the compaction request")
	}
	for _, raw := range jsonx.Slice(message["content"]) {
		if jsonx.Map(raw)["type"] == "refusal" {
			return "", errors.New("upstream refused the compaction request")
		}
	}
	summary := strings.TrimSpace(responseOutputText(response))
	if summary == "" {
		return "", errors.New("upstream returned an empty compaction summary")
	}
	return summary, nil
}
