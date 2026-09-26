// JSON-instruction fallback for Anthropic protocol. Anthropic's Messages
// API has native tool_use blocks, but MiniMax cn's current gateway does
// not pass them through. Instead we:
//
//   1. Inject the tool list into the system prompt.
//   2. Instruct the model to either emit a `{"tool_calls":[…]}` JSON
//      object (when it wants to call a tool) or just answer in plain
//      Chinese (when it has enough context).
//   3. Parse the response: if it's JSON with `tool_calls`, convert into
//      a ToolChatResult with ToolCalls; otherwise treat the whole reply
//      as assistant content.
//
// The fallback is deliberately strict on the *first* JSON object so
// chatty models that prefix a sentence ("好的，我来查一下...") still work
// — we scan the string for the first `{` and the matching `}`.

package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// toolSystemPreamble is prepended to the system prompt when tools are
// supplied to AnthropicClient.ToolChat. It explains the contract to the
// model and shows the JSON shape.
const toolSystemPreamble = `
你可以使用以下工具。在需要调用工具时，必须只输出一段严格的 JSON：
{"tool_calls":[{"id":"auto-1","name":"工具名","arguments":{...}}]}
其中 id 可用 "auto-N"（N 从 1 开始），arguments 必须是 JSON 对象。
如果不需要调用工具，直接用中文回答用户即可，不要输出 JSON。

工具列表：
`

// injectToolInstructions appends the tool list + the JSON contract to the
// system prompt. Returns the original prompt unchanged when tools is empty.
func injectToolInstructions(systemPrompt string, tools []ToolDef) string {
	if len(tools) == 0 {
		return systemPrompt
	}
	var sb strings.Builder
	sb.WriteString(systemPrompt)
	if systemPrompt != "" {
		sb.WriteString("\n\n")
	}
	sb.WriteString(toolSystemPreamble)
	for _, t := range tools {
		fmt.Fprintf(&sb, "- %s: %s\n  参数: %s\n", t.Name, t.Description, string(t.Parameters))
	}
	return sb.String()
}

// toolFallbackEnvelope is the JSON shape we look for. Only `tool_calls` is
// required; everything else is ignored so the model has room for slack.
type toolFallbackEnvelope struct {
	ToolCalls []struct {
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"tool_calls"`
	Final string `json:"final,omitempty"`
}

// parseToolFallback turns a raw model response into a ToolChatResult.
// Strategy:
//   1. Try to find a JSON object in the response (skip leading prose).
//   2. If the JSON contains tool_calls, return them with content="".
//   3. If the JSON is missing or malformed, the whole response is
//      treated as plain assistant text (the model decided to answer
//      directly without using tools).
func parseToolFallback(raw string) ToolChatResult {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ToolChatResult{Content: raw}
	}
	// Quick reject: anything that doesn't start with `{` is treated as
	// plain text, no JSON extraction.
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		// Could still be "好的，{...}" — try to find the first '{'.
		i := strings.Index(trimmed, "{")
		if i < 0 {
			return ToolChatResult{Content: raw}
		}
		trimmed = trimmed[i:]
	}

	// Find matching closing brace.
	end := findJSONEnd(trimmed)
	if end <= 0 {
		return ToolChatResult{Content: raw}
	}
	candidate := trimmed[:end+1]

	var env toolFallbackEnvelope
	if err := json.Unmarshal([]byte(candidate), &env); err != nil {
		return ToolChatResult{Content: raw}
	}
	if len(env.ToolCalls) == 0 {
		// JSON parsed but no tool_calls → treat original as final.
		return ToolChatResult{Content: raw}
	}
	out := ToolChatResult{}
	for i, tc := range env.ToolCalls {
		args := strings.TrimSpace(string(tc.Arguments))
		if args == "" {
			args = "{}"
		}
		id := tc.ID
		if id == "" {
			id = fmt.Sprintf("auto-%d", i+1)
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:        id,
			Name:      tc.Name,
			Arguments: args,
		})
	}
	// Surface any prose the model wrapped around the JSON so the
	// frontend can show partial thinking if it wants.
	if env.Final != "" {
		out.Content = env.Final
	}
	return out
}

// findJSONEnd returns the index of the matching '}' for the first '{' in
// the input, or -1 if no match. Tolerant of nested objects/strings.
func findJSONEnd(s string) int {
	depth := 0
	inStr := false
	escape := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escape {
			escape = false
			continue
		}
		if c == '\\' && inStr {
			escape = true
			continue
		}
		if c == '"' {
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}