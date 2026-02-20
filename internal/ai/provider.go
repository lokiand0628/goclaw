package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Provider 定义了 LLM 接口。
type Provider interface {
	// Stream 发送消息并回传流式事件。
	// tools 是 Anthropic JSON 模式格式的工具定义列表。
	Stream(ctx context.Context, req *Request) (<-chan Event, error)
}

// Request 代表单次 LLM 调用。
type Request struct {
	Model     string    `json:"model"`
	System    string    `json:"system,omitempty"`
	Messages  []Message `json:"messages"`
	Tools     []ToolDef `json:"tools,omitempty"`
	MaxTokens int       `json:"max_tokens,omitempty"`
}

// Message 是对话中的一个回合。
type Message struct {
	Role    string    `json:"role"`
	Content []Content `json:"content"`
}

// ModelConfig 定义模型的配置参数
type ModelConfig struct {
	MaxTokens     int
	ContextWindow int
	CostInput     float64 // 每百万 Token
	CostOutput    float64 // 每百万 Token
}

// 默认模型配置
var defaultModelConfig = ModelConfig{
	MaxTokens:     4096,
	ContextWindow: 128000,
}

// modelRegistry 存储已知模型的配置
// 参考 OpenClaw 的配置方式
var modelRegistry = map[string]ModelConfig{
	// Claude 3.5 Sonnet
	"claude-3-5-sonnet-20240620": {MaxTokens: 8192, ContextWindow: 200000},
	"claude-3-5-sonnet-latest":   {MaxTokens: 8192, ContextWindow: 200000},
	// Claude 3 Opus
	"claude-3-opus-20240229": {MaxTokens: 4096, ContextWindow: 200000},
	// Claude 3 Haiku
	"claude-3-haiku-20240307": {MaxTokens: 4096, ContextWindow: 200000},
	// GPT-4o
	"gpt-4o": {MaxTokens: 4096, ContextWindow: 128000},
	// GPT-4o-mini
	"gpt-4o-mini": {MaxTokens: 16384, ContextWindow: 128000},
	// DeepSeek V3
	"deepseek-chat":  {MaxTokens: 8192, ContextWindow: 64000},
	"deepseek-coder": {MaxTokens: 8192, ContextWindow: 64000},
	// MiniMax
	"abab6.5s-chat": {MaxTokens: 8192, ContextWindow: 245760},
	// Qwen (Aliyun Bailian)
	"qwen-max":             {MaxTokens: 8192, ContextWindow: 32000},
	"qwen-turbo":           {MaxTokens: 8192, ContextWindow: 32000},
	"qwen-plus":            {MaxTokens: 8192, ContextWindow: 32000},
	"qwen-long":            {MaxTokens: 6000, ContextWindow: 10000000},
	"qwen3-max-2026-01-23": {MaxTokens: 32768, ContextWindow: 262144},
	"qwen3-max":            {MaxTokens: 32768, ContextWindow: 262144},
	"qwen3-max-thinking":   {MaxTokens: 32768, ContextWindow: 262144},
	// Moonshot (Kimi)
	"moonshot-v1-8k":   {MaxTokens: 8192, ContextWindow: 8192},
	"moonshot-v1-32k":  {MaxTokens: 8192, ContextWindow: 32000},
	"moonshot-v1-128k": {MaxTokens: 8192, ContextWindow: 128000},
	"k2p5":             {MaxTokens: 8192, ContextWindow: 128000},
	// MiniMax-M2.5
	"minimax-m2.5": {MaxTokens: 8192, ContextWindow: 204800},
}

// getModelConfig 获取模型配置，支持模糊匹配
func getModelConfig(model string) ModelConfig {
	m := strings.ToLower(model)
	// 精确匹配
	if cfg, ok := modelRegistry[m]; ok {
		return cfg
	}
	// 模糊匹配
	if strings.Contains(m, "claude-3.5") || strings.Contains(m, "sonnet") {
		return ModelConfig{MaxTokens: 8192, ContextWindow: 200000}
	}
	if strings.Contains(m, "gpt-4") {
		return ModelConfig{MaxTokens: 4096, ContextWindow: 128000}
	}
	if strings.Contains(m, "deepseek") {
		return ModelConfig{MaxTokens: 8192, ContextWindow: 64000} // DeepSeek 往往支持长上下文
	}
	if strings.Contains(m, "minimax") {
		return ModelConfig{MaxTokens: 8192, ContextWindow: 245760}
	}
	if strings.Contains(m, "qwen") {
		return ModelConfig{MaxTokens: 8192, ContextWindow: 32000}
	}
	if strings.Contains(m, "moonshot") || strings.Contains(m, "k2") {
		return ModelConfig{MaxTokens: 8192, ContextWindow: 128000}
	}
	return defaultModelConfig
}

// modelMaxTokens 返回模型的最大输出 token 限制
func modelMaxTokens(model string) int {
	return getModelConfig(model).MaxTokens
}

// Content 是消息中的内容块。
type Content struct {
	Type string `json:"type"`
	// 用于文本块
	Text string `json:"text,omitempty"`
	// 用于思考块 (MiniMax/Anthropic)
	Thinking string `json:"thinking,omitempty"`
	// 用于 tool_use 块
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// 用于 tool_result 块
	ToolUseID string  `json:"tool_use_id,omitempty"`
	Content   *string `json:"content,omitempty"`
	IsError   bool    `json:"is_error,omitempty"` // Anthropic tool_result error flag
}

// TextMessage 是创建纯文本消息的便捷构造函数。
func TextMessage(role, text string) Message {
	return Message{Role: role, Content: []Content{{Type: "text", Text: text}}}
}

// ToolResultMessage 构造工具结果消息。
func ToolResultMessage(toolUseID, content string) Message {
	return Message{
		Role: "user",
		Content: []Content{{
			Type:      "tool_result",
			ToolUseID: toolUseID,
			Content:   &content,
		}},
	}
}

// ToolDef 描述了模型可调用的工具。
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// EventType 标识流式事件的类型。
type EventType string

const (
	EventText       EventType = "text"     // 增量文本片段
	EventThinking   EventType = "thinking" // 增量思考片段 (MiniMax/Anthropic)
	EventToolUse    EventType = "tool_use" // 完整的工具调用
	EventDone       EventType = "done"     // 流结束
	EventError      EventType = "error"    // 发生错误
	EventStopReason EventType = "stop"     // 停止原因 (end_turn, tool_use 等)
)

// Event 是 Provider 在流式传输期间发出的事件。
type Event struct {
	Type       EventType       `json:"type"`
	Text       string          `json:"text,omitempty"`        // EventText 使用
	Thinking   string          `json:"thinking,omitempty"`    // EventThinking 使用
	ToolUseID  string          `json:"tool_use_id,omitempty"` // EventToolUse 使用
	ToolName   string          `json:"tool_name,omitempty"`   // EventToolUse 使用
	ToolInput  json.RawMessage `json:"tool_input,omitempty"`  // EventToolUse 使用
	StopReason string          `json:"stop_reason,omitempty"` // EventStopReason / EventDone 使用
	Err        error           `json:"error,omitempty"`       // EventError 使用
}

// resolveEnvKey 尝试从环境变量获取 Key，如果传入的 apiKey 看起来像环境变量名
func resolveEnvKey(apiKey string, envVarName string) string {
	if apiKey == "" {
		return os.Getenv(envVarName)
	}
	// 如果 apiKey 格式像环境变量 (e.g. ${MY_KEY} or MY_KEY), 尝试解析
	trimmed := strings.TrimSpace(apiKey)
	if strings.HasPrefix(trimmed, "${") && strings.HasSuffix(trimmed, "}") {
		envKey := trimmed[2 : len(trimmed)-1]
		return os.Getenv(envKey)
	}
	// 简单的启发式：如果不包含空格且全大写/下划线，可能是环境变量名
	// 但为了安全，我们只在明确看起来像变量名时才尝试获取，或者由调用方显式处理
	// 这里主要处理空值回退
	return apiKey
}

// maskKey 用于日志脱敏
func maskKey(key string) string {
	if len(key) < 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

// AnthropicProvider 使用 Anthropic Messages API 实现 Provider
// (同时也兼容 MiniMax 等其他兼容 Anthropic API 的提供商)。
type AnthropicProvider struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
	options ProviderOptions
}

// ProviderOptions 配置 Provider 的行为
type ProviderOptions struct {
	MaxTokens     int
	ContextWindow int
}

func NewAnthropicProvider(apiKey, baseURL, model string, opts *ProviderOptions) *AnthropicProvider {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	// 自动回退查找环境变量
	finalKey := resolveEnvKey(apiKey, "ANTHROPIC_API_KEY")
	p := &AnthropicProvider{
		apiKey:  finalKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		client:  &http.Client{Timeout: 300 * time.Second},
	}
	if opts != nil {
		p.options = *opts
	}
	return p
}

func truncateDebug(s string, n int) string {
	if len(s) <= n {
		return s
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

func (p *AnthropicProvider) Stream(ctx context.Context, req *Request) (<-chan Event, error) {
	// 优先使用 options 中的 MaxTokens
	if p.options.MaxTokens > 0 {
		req.MaxTokens = p.options.MaxTokens
	}

	if req.MaxTokens == 0 {
		req.MaxTokens = modelMaxTokens(req.Model)
	}
	// 硬性上限保护，避免超出 API 限制导致 400
	cfg := getModelConfig(req.Model)
	if req.MaxTokens > cfg.MaxTokens {
		req.MaxTokens = cfg.MaxTokens
	}
	if req.Model == "" {
		req.Model = p.model
	}

	type anthropicRequest struct {
		Model     string    `json:"model"`
		System    string    `json:"system,omitempty"`
		Messages  []Message `json:"messages"`
		Tools     []ToolDef `json:"tools,omitempty"`
		MaxTokens int       `json:"max_tokens"`
		Stream    bool      `json:"stream"`
	}

	reqBody := anthropicRequest{
		Model:     req.Model,
		System:    req.System,
		Messages:  req.Messages,
		Tools:     req.Tools,
		MaxTokens: req.MaxTokens,
		Stream:    true,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	// 记录请求规模（有助于调试 400 错误）
	// fmt.Printf("📤 API 请求: model=%s, messages=%d, payload=%d bytes, max_tokens=%d, system=%d bytes, tools=%d, key=%s\n",
	// 	req.Model, len(req.Messages), len(body), req.MaxTokens, len(req.System), len(req.Tools), maskKey(p.apiKey))

	fullURL := p.baseURL
	if !strings.Contains(fullURL, "/messages") {
		fullURL = strings.TrimRight(fullURL, "/") + "/v1/messages"
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fullURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	// 兼容某些要求 Authorization 头的代理 (如 Aliyun Bailian 部分版本)
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("API 错误 %d: %s", resp.StatusCode, string(body))
	}

	ch := make(chan Event, 64)
	go p.parseSSE(ctx, resp.Body, ch)
	return ch, nil
}

// parseSSE 读取 SSE 流并发出事件。
func (p *AnthropicProvider) parseSSE(ctx context.Context, body io.ReadCloser, ch chan<- Event) {
	defer body.Close()
	defer close(ch)

	type deltaPayload struct {
		Type  string `json:"type"`
		Delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`         // text_delta 使用
			Thinking    string `json:"thinking"`     // thinking_delta 使用
			PartialJSON string `json:"partial_json"` // input_json_delta 使用 (Anthropic 规范)
			StopReason  string `json:"stop_reason"`
		} `json:"delta"`
		Index        int `json:"index"`
		ContentBlock struct {
			Type  string          `json:"type"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content_block"`
	}

	// 追踪部分的 tool_use 块
	type partialTool struct {
		id    string
		name  string
		input strings.Builder
	}
	tools := make(map[int]*partialTool)

	scanner := bufio.NewScanner(body)
	var eventType string

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			ch <- Event{Type: EventError, Err: ctx.Err()}
			return
		default:
		}

		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		// fmt.Printf("DEBUG: Anthropic SSE Data: %s\n", data) // 临时调试
		if data == "[DONE]" {
			ch <- Event{Type: EventDone}
			return
		}

		var payload deltaPayload
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			continue
		}

		// 某些代理不带 event: 行，或者 event 为 message，此时优先使用 JSON 内部的 type
		effectiveType := payload.Type
		if effectiveType == "" {
			effectiveType = eventType
		}

		switch effectiveType {
		case "content_block_start":
			cType := payload.ContentBlock.Type
			if cType == "tool_use" {
				tools[payload.Index] = &partialTool{
					id:   payload.ContentBlock.ID,
					name: payload.ContentBlock.Name,
				}
			} else if cType == "thinking" {
				// 思考块直接标记在索引中，不使用 partialTool 结构（因为它不需要解析 JSON）
				// 如果需要跨多个 delta 累积，也可以使用类似的 buffer
			}

		case "content_block_delta":
			switch payload.Delta.Type {
			case "text_delta", "text":
				if payload.Delta.Text != "" {
					ch <- Event{Type: EventText, Text: payload.Delta.Text}
				}
			case "thinking_delta", "thinking":
				if payload.Delta.Thinking != "" {
					ch <- Event{Type: EventThinking, Thinking: payload.Delta.Thinking}
				}
			case "input_json_delta":
				if t, ok := tools[payload.Index]; ok {
					// Anthropic 规范使用 partial_json；一些提供商使用 text 作为回退
					frag := payload.Delta.PartialJSON
					if frag == "" {
						frag = payload.Delta.Text
					}
					if frag != "" {
						t.input.WriteString(frag)
					}
				}
			}

		case "content_block_stop":
			if t, ok := tools[payload.Index]; ok {
				inputJSON := t.input.String()
				if inputJSON == "" {
					inputJSON = "{}"
				}
				ch <- Event{
					Type:      EventToolUse,
					ToolUseID: t.id,
					ToolName:  t.name,
					ToolInput: json.RawMessage(inputJSON),
				}
				delete(tools, payload.Index)
			}

		case "message_delta":
			if payload.Delta.StopReason != "" {
				ch <- Event{Type: EventStopReason, StopReason: payload.Delta.StopReason}
			}

		case "message_stop":
			ch <- Event{Type: EventDone}
			return
		}
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		ch <- Event{Type: EventError, Err: err}
	}
}

// OpenAIProvider 使用 OpenAI Chat Completions API 实现 Provider
// (支持任何兼容 OpenAI 协议的提供商，如 DeepSeek, GPT-4, etc)。
type OpenAIProvider struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
	options ProviderOptions
}

func NewOpenAIProvider(apiKey, baseURL, model string, opts *ProviderOptions) *OpenAIProvider {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	finalKey := resolveEnvKey(apiKey, "OPENAI_API_KEY")
	p := &OpenAIProvider{
		apiKey:  finalKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		client:  &http.Client{Timeout: 300 * time.Second},
	}
	if opts != nil {
		p.options = *opts
	}
	return p
}

func (p *OpenAIProvider) Stream(ctx context.Context, req *Request) (<-chan Event, error) {
	if req.Model == "" {
		req.Model = p.model
	}

	// 转换消息
	var messages []map[string]interface{}
	for _, m := range req.Messages {
		if m.Role == "user" || m.Role == "system" {
			var content strings.Builder
			var toolResults []map[string]interface{}
			for _, c := range m.Content {
				switch c.Type {
				case "text":
					content.WriteString(c.Text)
				case "tool_result":
					// OpenAI 的 tool_result 必须作为单独的 role="tool" 消息发送
					res := ""
					if c.Content != nil {
						res = *c.Content
					}
					if res == "" {
						res = "Success"
					}
					toolResults = append(toolResults, map[string]interface{}{
						"role":         "tool",
						"tool_call_id": c.ToolUseID,
						"content":      res,
					})
				}
			}

			// 如果这个 user 消息包含由 text 构建的部分，添加 user 消息
			if content.Len() > 0 {
				messages = append(messages, map[string]interface{}{
					"role":    m.Role,
					"content": content.String(),
				})
			} else if len(toolResults) == 0 {
				// 避免空 content
				messages = append(messages, map[string]interface{}{
					"role":    m.Role,
					"content": " ",
				})
			}

			// 追加 tool_results 作为独立的消息
			for _, tr := range toolResults {
				messages = append(messages, tr)
			}
		} else if m.Role == "assistant" {
			var content strings.Builder
			var toolCalls []map[string]interface{}
			for _, c := range m.Content {
				switch c.Type {
				case "text", "thinking":
					// 文本或思考内容直接加进去
					if c.Text != "" {
						content.WriteString(c.Text)
					}
					if c.Thinking != "" {
						content.WriteString("\n<thought>\n" + c.Thinking + "\n</thought>\n")
					}
				case "tool_use":
					// 构建 openai 格式的 tool_call
					toolCalls = append(toolCalls, map[string]interface{}{
						"id":   c.ID,
						"type": "function",
						"function": map[string]interface{}{
							"name":      c.Name,
							"arguments": string(c.Input),
						},
					})
				}
			}

			msg := map[string]interface{}{
				"role": "assistant",
			}
			if content.Len() > 0 {
				msg["content"] = content.String()
			}
			if len(toolCalls) > 0 {
				msg["tool_calls"] = toolCalls
			}
			messages = append(messages, msg)
		}
	}

	// 转换工具定义
	var openAITools []map[string]interface{}
	for _, t := range req.Tools {
		var schema map[string]interface{}
		_ = json.Unmarshal(t.InputSchema, &schema)
		openAITools = append(openAITools, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  schema,
			},
		})
	}

	payload := map[string]interface{}{
		"model":    req.Model,
		"messages": messages,
		"stream":   true,
	}

	if len(openAITools) > 0 {
		payload["tools"] = openAITools
	}

	body, _ := json.Marshal(payload)
	// fmt.Printf("📤 API 请求 (OpenAI): model=%s, messages=%d, payload=%d bytes, key=%s\n",
	// 	req.Model, len(req.Messages), len(body), maskKey(p.apiKey))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("OpenAI API 错误 %d: %s", resp.StatusCode, string(body))
	}

	ch := make(chan Event, 64)
	go p.parseSSE(resp.Body, ch)
	return ch, nil
}

func (p *OpenAIProvider) parseSSE(body io.ReadCloser, ch chan<- Event) {
	defer body.Close()
	defer close(ch)

	// 用于追踪多部分流式返回的 tool_calls
	type partialTool struct {
		id    string
		name  string
		input strings.Builder
	}
	tools := make(map[int]*partialTool)

	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			// 在结束前将所有未完成的 tool call 发送出去
			for _, t := range tools {
				ch <- Event{
					Type:      EventToolUse,
					ToolUseID: t.id,
					ToolName:  t.name,
					ToolInput: json.RawMessage(t.input.String()),
				}
			}
			ch <- Event{Type: EventDone}
			return
		}

		var payload struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			continue
		}

		if len(payload.Choices) > 0 {
			choice := payload.Choices[0]

			// 1. 处理普通文本
			if choice.Delta.Content != "" {
				ch <- Event{Type: EventText, Text: choice.Delta.Content}
			}

			// 2. 处理工具调用
			for _, tc := range choice.Delta.ToolCalls {
				t, exists := tools[tc.Index]
				if !exists {
					t = &partialTool{
						id:   tc.ID,
						name: tc.Function.Name,
					}
					tools[tc.Index] = t
				}
				if tc.Function.Arguments != "" {
					t.input.WriteString(tc.Function.Arguments)
				}
			}

			// 3. 停止原因或完成
			if choice.FinishReason != "" {
				// 如果是因为工具调用而停止，在此刻将拼接好的工具调用发送出去
				if choice.FinishReason == "tool_calls" {
					// 先发 tool_use，再发 stop_reason
					for idx, t := range tools {
						ch <- Event{
							Type:      EventToolUse,
							ToolUseID: t.id,
							ToolName:  t.name,
							ToolInput: json.RawMessage(t.input.String()),
						}
						delete(tools, idx)
					}
				}
				ch <- Event{Type: EventStopReason, StopReason: choice.FinishReason}
			}
		}
	}
}
