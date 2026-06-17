// Package streaming converts Anthropic /v1/messages SSE streams to the
// OpenAI /v1/chat/completions chunk format used by Cursor. The behaviour
// mirrors the original TypeScript converter:
//   - "ping" and "content_block_stop" events are dropped.
//   - text "content_block_start" events are dropped (only tool_use is kept).
//   - tool call argument deltas can be either incremental fragments or
//     accumulated partial_json — both are normalised to incremental chunks.
//   - On "message_stop" a final usage chunk and "data: [DONE]" are emitted.
package streaming

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Anthropic event payloads ---------------------------------------------------

type anthropicEvent struct {
	Type         string            `json:"type"`
	Index        int               `json:"index"`
	Message      *anthropicMessage `json:"message,omitempty"`
	ContentBlock *anthropicBlock   `json:"content_block,omitempty"`
	Delta        *anthropicDelta   `json:"delta,omitempty"`
	Model        string            `json:"model,omitempty"`
	StopReason   *string           `json:"stop_reason,omitempty"`
	Usage        *anthropicUsage   `json:"usage,omitempty"`
}

type anthropicMessage struct {
	ID         string          `json:"id"`
	Model      string          `json:"model"`
	StopReason *string         `json:"stop_reason,omitempty"`
	Usage      *anthropicUsage `json:"usage,omitempty"`
}

type anthropicBlock struct {
	Type  string         `json:"type"`
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Text  string         `json:"text"`
	Input map[string]any `json:"input,omitempty"`
}

type anthropicDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	PartialJSON string `json:"partial_json"`
	StopReason  string `json:"stop_reason"`
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// Anthropic non-streaming response ------------------------------------------

type AnthropicResponse struct {
	ID         string           `json:"id"`
	Model      string           `json:"model"`
	StopReason *string          `json:"stop_reason"`
	Content    []anthropicBlock `json:"content"`
	Usage      *anthropicUsage  `json:"usage"`
}

// OpenAI chunk payloads -----------------------------------------------------

// OpenAIChunk is a single chat.completion.chunk response.
type OpenAIChunk struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []openAIChoice `json:"choices"`
	Usage   *openAIUsage   `json:"usage,omitempty"`
}

type openAIChoice struct {
	Index        int         `json:"index"`
	Delta        openAIDelta `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

type openAIDelta struct {
	Role      string           `json:"role,omitempty"`
	Content   string           `json:"content,omitempty"`
	ToolCalls []openAIToolCall `json:"tool_calls,omitempty"`
}

type openAIToolCall struct {
	Index    int                     `json:"index"`
	ID       string                  `json:"id,omitempty"`
	Type     string                  `json:"type,omitempty"`
	Function *openAIToolCallFunction `json:"function,omitempty"`
}

type openAIToolCallFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// OpenAIResponse is the OpenAI non-streaming chat.completion payload.
type OpenAIResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role      string  `json:"role"`
			Content   *string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage openAIUsage `json:"usage"`
}

// State maintained across chunks of a single streaming response.
type State struct {
	toolCalls     map[int]*toolCall
	metrics       metrics
	openAIID      string
	openAICreated int64
}

type toolCall struct {
	ID        string
	Name      string
	Arguments string
}

type metrics struct {
	Model                    string
	StopReason               *string
	InputTokens              int
	OutputTokens             int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
	MessageID                string
}

// NewState creates a fresh streaming state.
func NewState() *State {
	return &State{
		toolCalls: map[int]*toolCall{},
	}
}

// Result is the outcome of processing one raw chunk: zero or more OpenAI
// chunks to forward to the client, and an optional Done flag indicating
// that the stream should be closed with a "data: [DONE]" sentinel.
type Result struct {
	Chunks []OpenAIChunk
	Done   bool
}

// ProcessChunk consumes an Anthropic SSE chunk (which may contain multiple
// data lines) and returns the OpenAI chunks to emit. enableLogging is
// accepted for parity with the TS implementation but currently unused.
func (s *State) ProcessChunk(chunk string) Result {
	var out Result
	if chunk == "" {
		return out
	}
	// Anthropic streams end events with \n\n; the upstream caller usually
	// hands us text already split on those boundaries, but we still process
	// every line to be safe.
	for _, line := range strings.Split(chunk, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "event:") {
			continue
		}
		const prefix = "data: "
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		payload := strings.TrimPrefix(line, prefix)
		if !strings.Contains(payload, "{") {
			continue
		}
		var ev anthropicEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		s.updateMetrics(&ev)
		if ch, ok := s.transform(&ev); ok {
			out.Chunks = append(out.Chunks, ch)
		}
		if ev.Type == "message_stop" {
			if usage := s.usageChunk(); usage != nil {
				out.Chunks = append(out.Chunks, *usage)
			}
			out.Done = true
		}
	}
	return out
}

func (s *State) updateMetrics(ev *anthropicEvent) {
	if ev.Type == "message_start" && ev.Message != nil {
		s.metrics.MessageID = ev.Message.ID
		if ev.Message.Model != "" {
			s.metrics.Model = ev.Message.Model
		}
	}
	if ev.Model != "" {
		s.metrics.Model = ev.Model
	}
	if ev.StopReason != nil {
		s.metrics.StopReason = ev.StopReason
	}
	if ev.Type == "message_delta" && ev.Delta != nil && ev.Delta.StopReason != "" {
		s.metrics.StopReason = strPtr(ev.Delta.StopReason)
	}
	if ev.Usage != nil {
		s.metrics.InputTokens += ev.Usage.InputTokens
		s.metrics.OutputTokens += ev.Usage.OutputTokens
		s.metrics.CacheCreationInputTokens += ev.Usage.CacheCreationInputTokens
		s.metrics.CacheReadInputTokens += ev.Usage.CacheReadInputTokens
	}
	if ev.Message != nil {
		if ev.Message.Model != "" {
			s.metrics.Model = ev.Message.Model
		}
		if ev.Message.Usage != nil {
			s.metrics.InputTokens += ev.Message.Usage.InputTokens
			s.metrics.OutputTokens += ev.Message.Usage.OutputTokens
			s.metrics.CacheCreationInputTokens += ev.Message.Usage.CacheCreationInputTokens
			s.metrics.CacheReadInputTokens += ev.Message.Usage.CacheReadInputTokens
		}
		if ev.Message.StopReason != nil {
			s.metrics.StopReason = ev.Message.StopReason
		}
	}
}

func strPtr(s string) *string { return &s }

func (s *State) transform(ev *anthropicEvent) (OpenAIChunk, bool) {
	switch ev.Type {
	case "ping", "content_block_stop":
		return OpenAIChunk{}, false

	case "message_start":
		if ev.Message == nil {
			return OpenAIChunk{}, false
		}
		openAIID := "chatcmpl-" + strings.TrimPrefix(ev.Message.ID, "msg_")
		s.openAIID = openAIID
		s.openAICreated = time.Now().Unix()
		content := ""
		return OpenAIChunk{
			ID:      openAIID,
			Object:  "chat.completion.chunk",
			Created: s.openAICreated,
			Model:   ev.Message.Model,
			Choices: []openAIChoice{{
				Index: 0,
				Delta: openAIDelta{Role: "assistant", Content: content},
			}},
		}, true

	case "content_block_start":
		if ev.ContentBlock == nil || ev.ContentBlock.Type != "tool_use" {
			return OpenAIChunk{}, false
		}
		s.toolCalls[ev.Index] = &toolCall{
			ID:   ev.ContentBlock.ID,
			Name: ev.ContentBlock.Name,
		}
		id := ev.ContentBlock.ID
		name := ev.ContentBlock.Name
		t := "function"
		idx := ev.Index
		return OpenAIChunk{
			ID:      s.openAIID,
			Object:  "chat.completion.chunk",
			Created: s.openAICreated,
			Model:   s.fallbackModel(),
			Choices: []openAIChoice{{
				Index: 0,
				Delta: openAIDelta{
					ToolCalls: []openAIToolCall{{
						Index: idx,
						ID:    id,
						Type:  t,
						Function: &openAIToolCallFunction{
							Name:      name,
							Arguments: "",
						},
					}},
				},
			}},
		}, true

	case "content_block_delta":
		if ev.Delta == nil {
			return OpenAIChunk{}, false
		}
		if ev.Delta.PartialJSON != "" {
			tc, ok := s.toolCalls[ev.Index]
			if !ok {
				return OpenAIChunk{}, false
			}
			var newPart string
			if tc.Arguments != "" && strings.HasPrefix(ev.Delta.PartialJSON, tc.Arguments) {
				// Anthropic re-sent the accumulated value; take only the new suffix.
				newPart = ev.Delta.PartialJSON[len(tc.Arguments):]
				tc.Arguments = ev.Delta.PartialJSON
			} else {
				// Pure fragment — just append.
				newPart = ev.Delta.PartialJSON
				tc.Arguments += ev.Delta.PartialJSON
			}
			idx := ev.Index
			arg := newPart
			return OpenAIChunk{
				ID:      s.openAIID,
				Object:  "chat.completion.chunk",
				Created: s.openAICreated,
				Model:   s.fallbackModel(),
				Choices: []openAIChoice{{
					Index: 0,
					Delta: openAIDelta{
						ToolCalls: []openAIToolCall{{
							Index:    idx,
							Function: &openAIToolCallFunction{Arguments: arg},
						}},
					},
				}},
			}, true
		}
		if ev.Delta.Text != "" {
			return OpenAIChunk{
				ID:      s.openAIID,
				Object:  "chat.completion.chunk",
				Created: s.openAICreated,
				Model:   s.fallbackModel(),
				Choices: []openAIChoice{{
					Index: 0,
					Delta: openAIDelta{Content: ev.Delta.Text},
				}},
			}, true
		}
		return OpenAIChunk{}, false

	case "message_delta":
		if ev.Delta == nil || ev.Delta.StopReason == "" {
			return OpenAIChunk{}, false
		}
		fr := stopReasonToFinish(ev.Delta.StopReason)
		return OpenAIChunk{
			ID:      s.openAIID,
			Object:  "chat.completion.chunk",
			Created: s.openAICreated,
			Model:   s.fallbackModel(),
			Choices: []openAIChoice{{Index: 0, Delta: openAIDelta{}, FinishReason: &fr}},
		}, true
	}
	return OpenAIChunk{}, false
}

func (s *State) fallbackModel() string {
	if s.metrics.Model == "" {
		return "claude-unknown"
	}
	return s.metrics.Model
}

func stopReasonToFinish(s string) string {
	switch s {
	case "end_turn":
		return "stop"
	case "tool_use":
		return "tool_calls"
	default:
		return s
	}
}

func (s *State) usageChunk() *OpenAIChunk {
	if s.metrics.InputTokens == 0 && s.metrics.OutputTokens == 0 {
		return nil
	}
	id := s.openAIID
	if id == "" {
		id = "chatcmpl-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return &OpenAIChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: s.openAICreated,
		Model:   s.fallbackModel(),
		Choices: []openAIChoice{{Index: 0, Delta: openAIDelta{}}},
		Usage: &openAIUsage{
			PromptTokens:     s.metrics.InputTokens,
			CompletionTokens: s.metrics.OutputTokens,
			TotalTokens:      s.metrics.InputTokens + s.metrics.OutputTokens,
		},
	}
}

// ConvertNonStreaming converts an Anthropic non-streaming response into
// the OpenAI chat.completion format.
func ConvertNonStreaming(in AnthropicResponse) OpenAIResponse {
	id := "chatcmpl-" + strings.TrimPrefix(in.ID, "msg_")
	fr := (*string)(nil)
	if in.StopReason != nil {
		v := stopReasonToFinish(*in.StopReason)
		fr = &v
	}
	usage := openAIUsage{}
	if in.Usage != nil {
		usage.PromptTokens = in.Usage.InputTokens
		usage.CompletionTokens = in.Usage.OutputTokens
		usage.TotalTokens = in.Usage.InputTokens + in.Usage.OutputTokens
	}
	resp := OpenAIResponse{
		ID:      id,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   fallback(in.Model, "claude-unknown"),
		Choices: []struct {
			Index   int `json:"index"`
			Message struct {
				Role      string  `json:"role"`
				Content   *string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason *string `json:"finish_reason"`
		}{{Index: 0, FinishReason: fr}},
		Usage: usage,
	}
	msg := &resp.Choices[0].Message
	msg.Role = "assistant"
	var text strings.Builder
	for _, b := range in.Content {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "tool_use":
			if b.ID == "" || b.Name == "" {
				continue
			}
			args := "{}"
			if len(b.Input) > 0 {
				if buf, err := json.Marshal(b.Input); err == nil {
					args = string(buf)
				}
			}
			msg.ToolCalls = append(msg.ToolCalls, struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			}{ID: b.ID, Type: "function", Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{Name: b.Name, Arguments: args}})
		}
	}
	if text.Len() > 0 {
		c := text.String()
		msg.Content = &c
	} else {
		msg.Content = nil
	}
	return resp
}

func fallback(s, fb string) string {
	if s == "" {
		return fb
	}
	return s
}

// EncodeOpenAIChunk marshals a chunk to the OpenAI SSE "data: <json>\n\n" form.
func EncodeOpenAIChunk(c OpenAIChunk) ([]byte, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	var out []byte
	out = append(out, []byte("data: ")...)
	out = append(out, b...)
	out = append(out, '\n', '\n')
	return out, nil
}

// ScanAnthropicSSE reads an Anthropic SSE stream and yields raw event
// payloads (one or more "\n\n" separated events joined back together).
// This is a convenience helper for the proxy handler.
func ScanAnthropicSSE(r io.Reader, yield func(payload string) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var buf strings.Builder
	flush := func() error {
		if buf.Len() == 0 {
			return nil
		}
		payload := buf.String()
		buf.Reset()
		return yield(payload)
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan sse: %w", err)
	}
	return flush()
}
