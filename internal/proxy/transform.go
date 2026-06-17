package proxy

import (
	"encoding/json"
	"strings"
)

// claudeCodeSystemHint is the prelude injected so Claude responds as if it
// were running inside Anthropic's official CLI. Cursor omits it by default
// when the user calls via the OpenAI-compatible endpoint, so the proxy
// restores it for any non-Claude-Code call site.
const claudeCodeSystemHint = "You are Claude Code, Anthropic's official CLI for Claude."

// TransformRequest rewrites a request body that came from the OpenAI-
// compatible /v1/chat/completions endpoint into the shape Anthropic's
// /v1/messages expects. The returned bool is true when the caller is
// expected to convert the response from Anthropic to OpenAI format.
//
// Mirrors the original TypeScript implementation:
//   - any "system"-role messages are pulled out of messages[] and merged
//     into the system[] prompt array, with the Claude Code hint prepended.
//   - opus models get max_tokens=32000, sonnet models get max_tokens=64000.
//   - an empty metadata block is created if absent.
func TransformRequest(body map[string]any) (transformToOpenAI bool, err error) {
	system, _ := body["system"].([]any)
	systemHasHint := false
	for _, s := range system {
		if m, ok := s.(map[string]any); ok {
			if t, _ := m["text"].(string); strings.Contains(t, claudeCodeSystemHint) {
				systemHasHint = true
				break
			}
		}
	}

	if systemHasHint {
		return false, nil
	}

	// OpenAI-style: extract system-role messages from messages[].
	if msgs, ok := body["messages"].([]any); ok {
		sysFromMsgs := make([]any, 0, len(msgs))
		nonSys := make([]any, 0, len(msgs))
		for _, m := range msgs {
			mm, ok := m.(map[string]any)
			if !ok {
				nonSys = append(nonSys, m)
				continue
			}
			if role, _ := mm["role"].(string); role == "system" {
				content, _ := mm["content"].(string)
				sysFromMsgs = append(sysFromMsgs, map[string]any{
					"type": "text",
					"text": content,
				})
				continue
			}
			nonSys = append(nonSys, m)
		}
		body["messages"] = nonSys

		if _, present := body["system"]; !present {
			body["system"] = []any{}
		}
		systemArr, _ := body["system"].([]any)
		newSystem := make([]any, 0, len(systemArr)+1+len(sysFromMsgs))
		newSystem = append(newSystem, map[string]any{"type": "text", "text": claudeCodeSystemHint})
		newSystem = append(newSystem, systemArr...)
		newSystem = append(newSystem, sysFromMsgs...)
		body["system"] = newSystem
	}

	if model, ok := body["model"].(string); ok {
		if strings.Contains(model, "opus") {
			body["max_tokens"] = 32000
		} else if strings.Contains(model, "sonnet") {
			body["max_tokens"] = 64000
		}
	}

	if _, present := body["metadata"]; !present {
		body["metadata"] = map[string]any{}
	}
	return true, nil
}

// EncodeBody marshals a transformed body to JSON for the upstream call.
func EncodeBody(body map[string]any) ([]byte, error) {
	return json.Marshal(body)
}
