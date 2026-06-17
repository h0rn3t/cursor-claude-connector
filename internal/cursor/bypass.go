// Package cursor implements the BYOK (Bring Your Own Key) bypass used
// when Cursor sends a probe request to validate the OpenAI-compatible
// endpoint. We respond with a canned chat.completion payload so the
// probe passes without ever contacting Anthropic.
package cursor

import (
	"encoding/json"
	"strings"
)

// BypassResponse is the canned OpenAI chat.completion returned to Cursor
// when it probes the BYOK endpoint. The field set mirrors exactly the
// payload Cursor expects during a key validation probe.
type BypassResponse struct {
	Choices []bypassChoice `json:"choices"`
	Created int64          `json:"created"`
	ID      string         `json:"id"`
	Model   string         `json:"model"`
	Object  string         `json:"object"`

	ServiceTier       string `json:"service_tier"`
	SystemFingerprint string `json:"system_fingerprint"`

	Usage bypassUsage `json:"usage"`
}

type bypassChoice struct {
	FinishReason string        `json:"finish_reason"`
	Index        int           `json:"index"`
	Logprobs     *any          `json:"logprobs"`
	Message      bypassMessage `json:"message"`
}

type bypassMessage struct {
	Annotations []string `json:"annotations"`
	Content     string   `json:"content"`
	Refusal     *any     `json:"refusal"`
	Role        string   `json:"role"`
}

type bypassUsage struct {
	CompletionTokens        int                     `json:"completion_tokens"`
	CompletionTokensDetails bypassCompletionDetails `json:"completion_tokens_details"`
	PromptTokens            int                     `json:"prompt_tokens"`
	PromptTokensDetails     bypassPromptDetails     `json:"prompt_tokens_details"`
	TotalTokens             int                     `json:"total_tokens"`
}

type bypassCompletionDetails struct {
	AcceptedPredictionTokens int `json:"accepted_prediction_tokens"`
	AudioTokens              int `json:"audio_tokens"`
	ReasoningTokens          int `json:"reasoning_tokens"`
	RejectedPredictionTokens int `json:"rejected_prediction_tokens"`
}

type bypassPromptDetails struct {
	AudioTokens  int `json:"audio_tokens"`
	CachedTokens int `json:"cached_tokens"`
}

// NewBypassResponse returns the canned payload Cursor expects.
func NewBypassResponse() BypassResponse {
	return BypassResponse{
		Choices: []bypassChoice{{
			FinishReason: "length",
			Index:        0,
			Logprobs:     nil,
			Message: bypassMessage{
				Annotations: []string{},
				Content:     "Of course! Please provide me with the text or",
				Refusal:     nil,
				Role:        "assistant",
			},
		}},
		Created:           1751755415,
		ID:                "chatcmpl-Bq5tXYkUOGxyRInJljhsBrlLP1066",
		Model:             "gpt-4o-2024-08-06",
		Object:            "chat.completion",
		ServiceTier:       "default",
		SystemFingerprint: "fp_a288987b44",
		Usage: bypassUsage{
			CompletionTokens:        10,
			CompletionTokensDetails: bypassCompletionDetails{},
			PromptTokens:            28,
			PromptTokensDetails:     bypassPromptDetails{},
			TotalTokens:             38,
		},
	}
}

// IsKeyCheckRequest reports whether the request body looks like Cursor's
// OpenAI key validation probe. Cursor sends either a gpt-4o test model
// or a "Test prompt using gpt-3.5-turbo" message.
func IsKeyCheckRequest(body map[string]any) bool {
	if model, ok := body["model"].(string); ok && strings.Contains(model, "gpt-4o") {
		return true
	}
	if msgs, ok := body["messages"].([]any); ok {
		for _, m := range msgs {
			mm, ok := m.(map[string]any)
			if !ok {
				continue
			}
			if c, ok := mm["content"].(string); ok && c == "Test prompt using gpt-3.5-turbo" {
				return true
			}
		}
	}
	return false
}

// MarshalBypass encodes the canned payload.
func MarshalBypass() ([]byte, error) {
	return json.Marshal(NewBypassResponse())
}
