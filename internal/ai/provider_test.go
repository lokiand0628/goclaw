package ai

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func collectEvents(ch <-chan Event) []Event {
	var events []Event
	for e := range ch {
		events = append(events, e)
	}
	return events
}

func TestNormalizeOpenAIBaseURL(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"https://api.openai.com/v1", "https://api.openai.com/v1"},
		{"https://api.openai.com/v1/chat/completions", "https://api.openai.com/v1"},
		{"https://dashscope.aliyuncs.com/compatible-mode/v1/messages", "https://dashscope.aliyuncs.com/compatible-mode/v1"},
		{"https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation", "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation"},
	}

	for _, tt := range tests {
		got := normalizeOpenAIBaseURL(tt.in)
		if got != tt.want {
			t.Fatalf("normalizeOpenAIBaseURL(%q)=%q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestAnthropicParseSSEAcceptsNoSpacePrefix(t *testing.T) {
	body := strings.Join([]string{
		"event:content_block_delta",
		"data:{\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}",
		"",
		"event:message_stop",
		"data:{\"type\":\"message_stop\"}",
		"",
	}, "\n")

	ch := make(chan Event, 8)
	p := &AnthropicProvider{}
	go p.parseSSE(context.Background(), io.NopCloser(strings.NewReader(body)), ch)

	events := collectEvents(ch)
	if len(events) == 0 {
		t.Fatal("expected events, got none")
	}

	foundText := false
	foundDone := false
	for _, e := range events {
		if e.Type == EventText && e.Text == "ok" {
			foundText = true
		}
		if e.Type == EventDone {
			foundDone = true
		}
	}

	if !foundText {
		t.Fatalf("expected text event 'ok', got %+v", events)
	}
	if !foundDone {
		t.Fatalf("expected done event, got %+v", events)
	}
}

func TestOpenAIParseSSEAcceptsNoSpacePrefix(t *testing.T) {
	body := strings.Join([]string{
		"data:{\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}",
		"",
		"data:[DONE]",
		"",
	}, "\n")

	ch := make(chan Event, 8)
	p := &OpenAIProvider{}
	go p.parseSSE(context.Background(), io.NopCloser(strings.NewReader(body)), ch)

	events := collectEvents(ch)
	if len(events) == 0 {
		t.Fatal("expected events, got none")
	}

	foundText := false
	for _, e := range events {
		if e.Type == EventText && e.Text == "ok" {
			foundText = true
		}
	}

	if !foundText {
		t.Fatalf("expected text event 'ok', got %+v", events)
	}
}

func TestConvertToolsToOpenAI(t *testing.T) {
	tools := []ToolDef{
		{
			Name:        "bash",
			Description: "run shell command",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`),
		},
		{
			Name:        "bad",
			Description: "bad schema fallback",
			InputSchema: json.RawMessage(`"not-an-object"`),
		},
	}

	converted := convertToolsToOpenAI(tools)
	if len(converted) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(converted))
	}
	if converted[0].Type != "function" {
		t.Fatalf("expected tool type=function, got %q", converted[0].Type)
	}
	if converted[0].Function.Name != "bash" {
		t.Fatalf("expected tool name bash, got %q", converted[0].Function.Name)
	}

	var schema map[string]any
	if err := json.Unmarshal(converted[1].Function.Parameters, &schema); err != nil {
		t.Fatalf("fallback schema should be valid JSON object, got err: %v", err)
	}
	if got, ok := schema["type"].(string); !ok || got != "object" {
		t.Fatalf("expected fallback type=object, got %#v", schema["type"])
	}
}

func TestConvertMessagesToOpenAI(t *testing.T) {
	toolResult := "ok"
	msgs := []Message{
		{
			Role: "user",
			Content: []Content{
				{Type: "text", Text: "hello"},
			},
		},
		{
			Role: "assistant",
			Content: []Content{
				{Type: "thinking", Thinking: "hidden"},
				{Type: "text", Text: "running tool"},
				{Type: "tool_use", ID: "call_1", Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)},
			},
		},
		{
			Role: "user",
			Content: []Content{
				{Type: "tool_result", ToolUseID: "call_1", Content: &toolResult},
			},
		},
	}

	got := convertMessagesToOpenAI(msgs)
	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}
	if got[0].Role != "user" || got[0].Content != "hello" {
		t.Fatalf("unexpected first message: %+v", got[0])
	}
	if got[1].Role != "assistant" {
		t.Fatalf("expected assistant message, got %+v", got[1])
	}
	if len(got[1].ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %+v", got[1].ToolCalls)
	}
	if got[1].ToolCalls[0].Function.Arguments != `{"command":"ls"}` {
		t.Fatalf("unexpected tool arguments: %q", got[1].ToolCalls[0].Function.Arguments)
	}
	if got[2].Role != "tool" || got[2].ToolCallID != "call_1" || got[2].Content != "ok" {
		t.Fatalf("unexpected tool result message: %+v", got[2])
	}
}

func TestSanitizeAnthropicMessagesDropsThinking(t *testing.T) {
	msgs := []Message{
		{
			Role: "assistant",
			Content: []Content{
				{Type: "thinking", Thinking: "internal"},
				{Type: "text", Text: "visible"},
			},
		},
	}

	got := sanitizeAnthropicMessages(msgs)
	if len(got) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got))
	}
	if len(got[0].Content) != 1 {
		t.Fatalf("expected thinking block removed, got %+v", got[0].Content)
	}
	if got[0].Content[0].Type != "text" || got[0].Content[0].Text != "visible" {
		t.Fatalf("unexpected sanitized content: %+v", got[0].Content[0])
	}
}
