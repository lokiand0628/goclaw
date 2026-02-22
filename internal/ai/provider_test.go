package ai

import (
	"context"
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

