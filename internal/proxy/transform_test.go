package proxy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTransformRequest_OpenAIShape(t *testing.T) {
	body := map[string]any{
		"model": "claude-sonnet-4-5",
		"messages": []any{
			map[string]any{"role": "system", "content": "be brief"},
			map[string]any{"role": "user", "content": "hi"},
		},
	}
	transform, err := TransformRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if !transform {
		t.Fatal("expected transform=true for non-Claude-Code call")
	}
	sys, ok := body["system"].([]any)
	if !ok || len(sys) != 2 {
		t.Fatalf("system should have 2 entries; got: %#v", body["system"])
	}
	if !strings.Contains(sys[0].(map[string]any)["text"].(string), "You are Claude Code") {
		t.Errorf("first system entry should be the Claude Code hint; got %v", sys[0])
	}
	if sys[1].(map[string]any)["text"] != "be brief" {
		t.Errorf("system entry from messages should be appended; got %v", sys[1])
	}
	if max, ok := body["max_tokens"]; !ok || max != 64000 {
		t.Errorf("sonnet should set max_tokens=64000; got %v", max)
	}
	msgs := body["messages"].([]any)
	if len(msgs) != 1 {
		t.Errorf("system message should have been moved out; messages=%d", len(msgs))
	}
}

func TestTransformRequest_ClaudeCodeShapeUnchanged(t *testing.T) {
	body := map[string]any{
		"model": "claude-sonnet-4-5",
		"system": []any{
			map[string]any{"type": "text", "text": "You are Claude Code, Anthropic's official CLI for Claude."},
			map[string]any{"type": "text", "text": "be brief"},
		},
		"messages": []any{
			map[string]any{"role": "user", "content": "hi"},
		},
	}
	transform, err := TransformRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if transform {
		t.Fatal("expected transform=false when Claude Code hint is already present")
	}
}

func TestTransformRequest_OpusMaxTokens(t *testing.T) {
	body := map[string]any{
		"model":    "claude-opus-4-1",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}
	if _, err := TransformRequest(body); err != nil {
		t.Fatal(err)
	}
	if body["max_tokens"] != 32000 {
		t.Errorf("opus should set max_tokens=32000; got %v", body["max_tokens"])
	}
}

func TestEncodeBody(t *testing.T) {
	body := map[string]any{"a": 1}
	b, err := EncodeBody(body)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back["a"].(float64) != 1 {
		t.Errorf("round-trip: %v", back)
	}
}
