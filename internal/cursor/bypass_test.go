package cursor

import (
	"encoding/json"
	"testing"
)

func TestIsKeyCheckRequest(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
		want bool
	}{
		{"gpt-4o model", map[string]any{"model": "gpt-4o-2024-08-06"}, true},
		{"gpt-3.5 test message", map[string]any{
			"model":    "gpt-3.5-turbo",
			"messages": []any{map[string]any{"role": "user", "content": "Test prompt using gpt-3.5-turbo"}},
		}, true},
		{"claude request", map[string]any{"model": "claude-sonnet-4-5"}, false},
		{"empty", map[string]any{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsKeyCheckRequest(tc.body); got != tc.want {
				t.Errorf("want %v, got %v", tc.want, got)
			}
		})
	}
}

func TestBypassResponseRoundTrip(t *testing.T) {
	r := NewBypassResponse()
	if r.Object != "chat.completion" {
		t.Errorf("object: %s", r.Object)
	}
	if r.Model != "gpt-4o-2024-08-06" {
		t.Errorf("model: %s", r.Model)
	}
	if len(r.Choices) != 1 {
		t.Fatalf("choices: %d", len(r.Choices))
	}
	if r.Choices[0].Message.Content == "" {
		t.Errorf("content should be non-empty")
	}
	if r.Usage.TotalTokens != 38 {
		t.Errorf("total_tokens: %d", r.Usage.TotalTokens)
	}

	// Encoding must produce valid JSON with the same shape Cursor expects.
	b, err := MarshalBypass()
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	if err := json.Unmarshal(b, &generic); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if generic["id"] != "chatcmpl-Bq5tXYkUOGxyRInJljhsBrlLP1066" {
		t.Errorf("id: %v", generic["id"])
	}
}
