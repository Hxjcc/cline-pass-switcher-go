package responses

import (
	"encoding/json"
	"strings"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
)

func normalizedImageDetail(value any) string {
	detail := jsonx.String(value)
	if detail == "original" {
		return "high"
	}
	switch detail {
	case "auto", "low", "high":
		return detail
	default:
		return ""
	}
}

func imageURLPart(part map[string]any) (map[string]any, bool) {
	typeName := jsonx.String(part["type"])
	if typeName != "input_image" && typeName != "image_url" && typeName != "image" && typeName != "output_image" {
		return nil, false
	}
	detail := normalizedImageDetail(part["detail"])
	var imageURL any
	if value := part["image_url"]; value != nil {
		imageURL = value
	} else if value := jsonx.String(part["url"]); value != "" {
		imageURL = value
	} else if value := jsonx.String(part["image"]); value != "" {
		imageURL = value
	} else if data := jsonx.String(part["data"]); data != "" {
		if strings.HasPrefix(data, "data:") {
			imageURL = data
		} else {
			mimeType := jsonx.String(part["mimeType"])
			if mimeType == "" {
				mimeType = jsonx.String(part["mime_type"])
			}
			if mimeType == "" {
				mimeType = "image/png"
			}
			imageURL = "data:" + mimeType + ";base64," + data
		}
	}
	if imageURL == nil {
		return nil, false
	}

	switch typed := imageURL.(type) {
	case string:
		if typed == "" {
			return nil, false
		}
		value := map[string]any{"url": typed}
		if detail != "" {
			value["detail"] = detail
		}
		return map[string]any{"type": "image_url", "image_url": value}, true
	case map[string]any:
		value := make(map[string]any, len(typed)+1)
		for key, child := range typed {
			value[key] = child
		}
		if detail != "" && value["detail"] == nil {
			value["detail"] = detail
		}
		if jsonx.String(value["url"]) == "" {
			return nil, false
		}
		return map[string]any{"type": "image_url", "image_url": value}, true
	default:
		return nil, false
	}
}

func chatContentFromResponseContent(content any) any {
	if text, ok := content.(string); ok {
		return text
	}
	items := jsonx.Slice(content)
	if items == nil {
		return ""
	}
	parts := make([]any, 0, len(items))
	allText := true
	for _, value := range items {
		part := jsonx.Map(value)
		if part == nil {
			continue
		}
		typeName := jsonx.String(part["type"])
		switch typeName {
		case "input_text", "output_text", "text":
			if text := jsonx.String(part["text"]); text != "" {
				parts = append(parts, map[string]any{"type": "text", "text": text})
			}
		case "refusal":
			if text := jsonx.String(part["refusal"]); text != "" {
				parts = append(parts, map[string]any{"type": "text", "text": text})
			}
		case "input_image", "image_url", "image", "output_image":
			if image, ok := imageURLPart(part); ok {
				parts = append(parts, image)
				allText = false
			}
		case "input_file":
			name := jsonx.String(part["filename"])
			if name == "" {
				name = jsonx.String(part["file_id"])
			}
			if name == "" {
				name = "attachment"
			}
			parts = append(parts, map[string]any{"type": "text", "text": "[file input omitted: " + name + "]"})
		}
	}
	if len(parts) == 0 {
		return ""
	}
	if allText {
		var builder strings.Builder
		for _, value := range parts {
			builder.WriteString(jsonx.String(jsonx.Map(value)["text"]))
		}
		return builder.String()
	}
	return parts
}

type outputParts struct {
	Text   []string
	Images []any
}

func (parts *outputParts) addText(value string) {
	if value != "" {
		parts.Text = append(parts.Text, value)
	}
}

// Tool results are opaque data unless they explicitly contain protocol content
// blocks. A business object with a field named content/output/text is not a
// wrapper and must retain every field.
func isToolContent(value any, depth int) bool {
	if depth > 20 {
		return false
	}
	switch typed := value.(type) {
	case []any:
		if len(typed) == 0 {
			return false
		}
		for _, child := range typed {
			if !isToolContent(child, depth+1) {
				return false
			}
		}
		return true
	case map[string]any:
		if _, ok := imageURLPart(typed); ok {
			return true
		}
		switch jsonx.String(typed["type"]) {
		case "input_text", "output_text", "text":
			_, ok := typed["text"].(string)
			return ok
		}
		for _, key := range []string{"content", "output"} {
			if isToolContent(typed[key], depth+1) {
				return true
			}
		}
	}
	return false
}

func (parts *outputParts) addJSON(value any) {
	if raw, err := json.Marshal(value); err == nil {
		parts.addText(string(raw))
	}
}

func collectToolOutput(value any, parts *outputParts, depth int) {
	if value == nil {
		return
	}
	if depth > 20 {
		parts.addJSON(value)
		return
	}
	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
			(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
			var decoded any
			decoder := json.NewDecoder(strings.NewReader(trimmed))
			decoder.UseNumber()
			if decoder.Decode(&decoded) == nil && json.Valid([]byte(trimmed)) && isToolContent(decoded, depth+1) {
				media := outputParts{}
				collectToolOutput(decoded, &media, depth+1)
				// Only decode a string when needed to deliver actual images.
				// Otherwise preserve its whitespace, JSON shape and number spelling.
				if len(media.Images) > 0 {
					parts.Text = append(parts.Text, media.Text...)
					parts.Images = append(parts.Images, media.Images...)
					return
				}
			}
		}
		parts.addText(typed)
	case []any:
		if !isToolContent(typed, depth) {
			parts.addJSON(typed)
			return
		}
		for _, child := range typed {
			collectToolOutput(child, parts, depth+1)
		}
	case map[string]any:
		if image, ok := imageURLPart(typed); ok {
			parts.Images = append(parts.Images, image)
			return
		}
		typeName := jsonx.String(typed["type"])
		if typeName == "input_text" || typeName == "output_text" || typeName == "text" {
			parts.addText(jsonx.String(typed["text"]))
			return
		}
		for _, key := range []string{"content", "output"} {
			if child := typed[key]; isToolContent(child, depth+1) {
				collectToolOutput(child, parts, depth+1)
				// MCP wrappers can carry isError, structuredContent, cursors,
				// and other data beside their content blocks.
				extra := make(map[string]any, len(typed)-1)
				for name, field := range typed {
					if name != key {
						extra[name] = field
					}
				}
				if len(extra) > 0 {
					parts.addJSON(extra)
				}
				return
			}
		}
		parts.addJSON(typed)
	default:
		parts.addJSON(typed)
	}
}

func parseToolOutput(value any) outputParts {
	parts := outputParts{}
	collectToolOutput(value, &parts, 0)
	return parts
}

func messageFromResponseItem(item map[string]any) map[string]any {
	role := jsonx.String(item["role"])
	if role == "developer" {
		role = "system"
	}
	switch role {
	case "system", "assistant", "user":
	default:
		role = "user"
	}
	message := map[string]any{"role": role, "content": chatContentFromResponseContent(item["content"])}
	if role == "assistant" {
		content := make([]any, 0)
		var refusal strings.Builder
		for _, raw := range jsonx.Slice(item["content"]) {
			part := jsonx.Map(raw)
			if part["type"] == "refusal" {
				refusal.WriteString(jsonx.String(part["refusal"]))
			} else {
				content = append(content, raw)
			}
		}
		if refusal.Len() > 0 {
			message["refusal"] = refusal.String()
			message["content"] = chatContentFromResponseContent(content)
			if len(content) == 0 {
				message["content"] = nil
			}
		}
	}
	return message
}

func (context *Context) chatToolName(name, namespace string) string {
	if value := context.originalToChat[originalToolKey(namespace, name)]; value != "" {
		return value
	}
	if value := context.originalToChat[name]; value != "" {
		return value
	}
	if namespace != "" {
		return safeToolName(namespace + "__" + name)
	}
	return safeToolName(name)
}

func functionCallFromResponseItem(item map[string]any, context *Context) map[string]any {
	callID := jsonx.String(item["call_id"])
	if callID == "" {
		callID = jsonx.String(item["id"])
	}
	typeName := jsonx.String(item["type"])
	name := jsonx.String(item["name"])
	namespace := jsonx.String(item["namespace"])
	chatName := context.chatToolName(name, namespace)
	arguments := jsonx.String(item["arguments"])
	if typeName == "custom_tool_call" {
		argumentsRaw, _ := json.Marshal(map[string]any{customToolInputKey: jsonx.String(item["input"])})
		arguments = string(argumentsRaw)
	} else if typeName == "tool_search_call" {
		chatName = toolSearchName
		if arguments == "" {
			argumentsRaw, _ := json.Marshal(item["arguments"])
			arguments = string(argumentsRaw)
		}
	}
	if arguments == "" || arguments == "null" {
		arguments = "{}"
	}
	return map[string]any{
		"id":   callID,
		"type": "function",
		"function": map[string]any{
			"name":      chatName,
			"arguments": arguments,
		},
	}
}

// orphanToolOutputMessage renders a tool result whose call is missing from the
// request as user-visible content. ChatGPT Desktop writes delegation hand-offs
// and host-side tool results into a thread without the matching call item, and
// Chat Completions cannot carry a tool message that follows no tool call.
func orphanToolOutputMessage(item map[string]any, text string, images []any) map[string]any {
	body := text
	if isDelegationToolOutput(item, text) {
		body = delegationInput(text)
	} else {
		name := strings.TrimSpace(jsonx.String(item["name"]))
		marker := "[tool result without a recorded call"
		if name != "" {
			marker += " " + name
		}
		marker += "]\n"
		body = marker + text
	}
	if len(images) == 0 {
		return map[string]any{"role": "user", "content": body}
	}
	parts := []any{map[string]any{"type": "text", "text": body}}
	parts = append(parts, images...)
	return map[string]any{"role": "user", "content": parts}
}

// isDelegationToolOutput recognizes the cross-task hand-off payload that the
// desktop client stores at the head of a delegated thread.
func isDelegationToolOutput(item map[string]any, text string) bool {
	if jsonx.String(item["name"]) == "create_thread" || jsonx.String(item["namespace"]) == "codex_app" {
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(text), "<codex_delegation>")
}

// delegationInput unwraps the XML envelope so the model receives the delegated
// prompt as an ordinary user message; the envelope itself is client bookkeeping.
func delegationInput(text string) string {
	start := strings.Index(text, "<input>")
	end := strings.LastIndex(text, "</input>")
	if start < 0 || end <= start {
		return strings.TrimSpace(text)
	}
	inner := text[start+len("<input>") : end]
	if strings.Contains(inner, "&") {
		inner = strings.NewReplacer(
			"&lt;", "<", "&gt;", ">", "&apos;", "'", "&amp;", "&",
		).Replace(inner)
	}
	return strings.TrimSpace(inner)
}

func (context *Context) toolChoiceToChat(value any) any {
	if text, ok := value.(string); ok {
		switch text {
		case "auto", "none", "required":
			return text
		default:
			return nil
		}
	}
	choice := jsonx.Map(value)
	if choice == nil {
		return nil
	}
	name := jsonx.String(choice["name"])
	if name == "" {
		return nil
	}
	chatName := context.chatToolName(name, jsonx.String(choice["namespace"]))
	if jsonx.String(choice["type"]) == "tool_search" {
		chatName = toolSearchName
	}
	return map[string]any{"type": "function", "function": map[string]any{"name": chatName}}
}
