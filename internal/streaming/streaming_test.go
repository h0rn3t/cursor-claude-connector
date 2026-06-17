package streaming

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// chunkFor builds a single SSE event payload as the upstream would emit.
func chunkFor(t *testing.T, ev any) string {
	t.Helper()
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return "data: " + string(b) + "\n\n"
}

func TestProcessChunk_StreamingTextFlow(t *testing.T) {
	st := NewState()

	chunks := []string{
		chunkFor(t, map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":    "msg_01",
				"model": "claude-sonnet-4-5",
				"usage": map[string]any{"input_tokens": 5, "output_tokens": 0},
			},
		}),
		chunkFor(t, map[string]any{
			"type":          "content_block_start",
			"index":         0,
			"content_block": map[string]any{"type": "text", "text": ""},
		}),
		chunkFor(t, map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]any{"type": "text_delta", "text": "hello "},
		}),
		chunkFor(t, map[string]any{
			"type":  "content_block_delta",
			"index": 0,
			"delta": map[string]any{"type": "text_delta", "text": "world"},
		}),
		chunkFor(t, map[string]any{
			"type":  "message_delta",
			"delta": map[string]any{"stop_reason": "end_turn"},
		}),
		chunkFor(t, map[string]any{
			"type": "message_stop",
		}),
	}

	var got []string
	done := false
	for _, c := range chunks {
		r := st.ProcessChunk(c)
		for _, oc := range r.Chunks {
			buf, _ := EncodeOpenAIChunk(oc)
			got = append(got, string(buf))
		}
		if r.Done {
			done = true
		}
	}
	if !done {
		t.Fatalf("expected Done flag to be set after message_stop")
	}
	combined := strings.Join(got, "")

	// First chunk should declare role=assistant and the OpenAI id.
	if !strings.Contains(combined, `"role":"assistant"`) {
		t.Errorf("expected role=assistant in initial chunk; got: %s", combined)
	}
	if !strings.Contains(combined, `"chatcmpl-01"`) {
		t.Errorf("expected chatcmpl- id derived from msg_id; got: %s", combined)
	}
	// Text deltas should appear.
	if !strings.Contains(combined, `"content":"hello "`) {
		t.Errorf("missing first text delta; got: %s", combined)
	}
	if !strings.Contains(combined, `"content":"world"`) {
		t.Errorf("missing second text delta; got: %s", combined)
	}
	// Final usage chunk should reference total input tokens.
	if !strings.Contains(combined, `"prompt_tokens":5`) {
		t.Errorf("expected usage chunk to report prompt_tokens=5; got: %s", combined)
	}
	// Stop reason should be translated to "stop".
	if !strings.Contains(combined, `"finish_reason":"stop"`) {
		t.Errorf("expected finish_reason=stop in final delta; got: %s", combined)
	}
}

func TestProcessChunk_ToolUseDeltasAreIncremental(t *testing.T) {
	st := NewState()

	// Anthropic sends the start block + a sequence of accumulated partial_json
	// strings. The converter must turn them into incremental OpenAI deltas.
	chunks := []string{
		chunkFor(t, map[string]any{
			"type":    "message_start",
			"message": map[string]any{"id": "msg_02", "model": "claude-sonnet-4-5"},
		}),
		chunkFor(t, map[string]any{
			"type":  "content_block_start",
			"index": 1,
			"content_block": map[string]any{
				"type": "tool_use",
				"id":   "toolu_1",
				"name": "get_weather",
			},
		}),
		chunkFor(t, map[string]any{
			"type":  "content_block_delta",
			"index": 1,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": `{"city":"`},
		}),
		chunkFor(t, map[string]any{
			"type":  "content_block_delta",
			"index": 1,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": `{"city":"Paris`},
		}),
		chunkFor(t, map[string]any{
			"type":  "content_block_delta",
			"index": 1,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": `{"city":"Paris"}`},
		}),
		chunkFor(t, map[string]any{"type": "message_stop"}),
	}

	var toolArgs []string
	for _, c := range chunks {
		r := st.ProcessChunk(c)
		for _, oc := range r.Chunks {
			for _, ch := range oc.Choices {
				for _, tc := range ch.Delta.ToolCalls {
					if tc.Function != nil {
						toolArgs = append(toolArgs, tc.Function.Arguments)
					}
				}
			}
		}
	}

	// The first entry is the content_block_start (empty arguments); the
	// following three are the incremental partial_json deltas.
	if len(toolArgs) != 4 {
		t.Fatalf("expected 4 tool-arg entries (1 start + 3 deltas); got %d: %#v", len(toolArgs), toolArgs)
	}
	if toolArgs[0] != "" {
		t.Errorf("start entry should have empty args; got %q", toolArgs[0])
	}
	if toolArgs[1] != `{"city":"` {
		t.Errorf("first delta mismatch: %q", toolArgs[1])
	}
	if toolArgs[2] != `Paris` {
		t.Errorf("second delta mismatch: %q", toolArgs[2])
	}
	if toolArgs[3] != `"}` {
		t.Errorf("third delta mismatch: %q", toolArgs[3])
	}
}

func TestConvertNonStreaming(t *testing.T) {
	in := AnthropicResponse{
		ID:    "msg_99",
		Model: "claude-sonnet-4-5",
		Content: []anthropicBlock{
			{Type: "text", Text: "hi"},
			{Type: "tool_use", ID: "t1", Name: "echo", Input: map[string]any{"x": 1}},
		},
		StopReason: strPtrLocal("end_turn"),
		Usage:      &anthropicUsage{InputTokens: 3, OutputTokens: 7},
	}
	out := ConvertNonStreaming(in)
	if out.ID != "chatcmpl-99" {
		t.Errorf("id: %s", out.ID)
	}
	if out.Choices[0].FinishReason == nil || *out.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason: %v", out.Choices[0].FinishReason)
	}
	if out.Choices[0].Message.Content == nil || *out.Choices[0].Message.Content != "hi" {
		t.Errorf("content: %v", out.Choices[0].Message.Content)
	}
	if len(out.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("tool calls: %d", len(out.Choices[0].Message.ToolCalls))
	}
	tc := out.Choices[0].Message.ToolCalls[0]
	if tc.ID != "t1" || tc.Type != "function" {
		t.Errorf("tool call header: %+v", tc)
	}
	if tc.Function.Name != "echo" || tc.Function.Arguments != `{"x":1}` {
		t.Errorf("tool call fn: %+v", tc.Function)
	}
	if out.Usage.TotalTokens != 10 {
		t.Errorf("total_tokens: %d", out.Usage.TotalTokens)
	}
}

func strPtrLocal(s string) *string { return &s }

func TestScanAnthropicSSE(t *testing.T) {
	src := strings.Join([]string{
		"event: message_start",
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\"}}",
		"",
		"data: {\"type\":\"message_stop\"}",
		"",
	}, "\n")
	var got []string
	err := ScanAnthropicSSE(bytes.NewBufferString(src), func(payload string) error {
		got = append(got, payload)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 payloads, got %d: %q", len(got), got)
	}
	if !strings.Contains(got[0], "message_start") {
		t.Errorf("first payload wrong: %q", got[0])
	}
	if !strings.Contains(got[1], "message_stop") {
		t.Errorf("second payload wrong: %q", got[1])
	}
}
