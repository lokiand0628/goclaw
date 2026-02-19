package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"goclaw/internal/ai"
	"goclaw/internal/config"
	"goclaw/internal/store"
	"goclaw/internal/tools"
	"goclaw/internal/wslib"

	"golang.org/x/sync/semaphore"
)

// Agent 管理单个代理的代理循环。
type Agent struct {
	id            string
	name          string
	provider      ai.Provider
	store         store.Store
	registry      *tools.Registry
	loader        loaderIface
	sem           *semaphore.Weighted
	contextWindow int
	projectRoot   string
	model         string
	cfg           *config.Config // 新增：配置引用

	// 会话级历史摘要（内存存储，重启后消失；重启后历史消息从 DB 完整重新加载，无需持久化）
	// key: sessionID, value: 该会话的压缩摘要文本
	sessionSummaries map[string]string
	summaryMu        sync.RWMutex
}

// Config 是 Agent 的运行时配置。
type Config struct {
	ID            string
	Name          string
	Provider      ai.Provider
	Store         store.Store
	Registry      *tools.Registry
	Loader        loaderIface
	Concurrency   int    // 最大并行调用数
	ContextWindow int    // 要保留的最大消息数
	ProjectRoot   string // 用于守护检查
	Model         string
	Cfg           *config.Config // 新增：全局配置
}

func New(cfg Config) *Agent {
	conc := cfg.Concurrency
	if conc <= 0 {
		conc = 4
	}
	return &Agent{
		id:               cfg.ID,
		name:             cfg.Name,
		provider:         cfg.Provider,
		store:            cfg.Store,
		registry:         cfg.Registry,
		loader:           cfg.Loader,
		sem:              semaphore.NewWeighted(int64(conc)),
		contextWindow:    cfg.ContextWindow,
		projectRoot:      cfg.ProjectRoot,
		model:            cfg.Model,
		cfg:              cfg.Cfg,
		sessionSummaries: make(map[string]string),
	}
}

// estimateTokens 粗略估算消息列表的 token 数量。
// 非 ASCII 字符（中文等）约 1 token/字，ASCII 约 1 token/4字符。
// 这是保守估算，用于触发压缩，不追求精确。
func estimateTokens(msgs []*store.Message) int {
	total := 0
	for _, m := range msgs {
		for _, r := range m.Content {
			if r > 127 {
				total += 2 // 非 ASCII（中日韩等）: 约 1 token
			} else {
				total++ // ASCII 字符：~4 字符/token，这里每字符算 0.25
			}
		}
		if m.Type == "json" {
			total += len(m.Content) / 8 // JSON 结构有额外开销
		}
	}
	return total / 4 // 最终除以 4 得到近似 token 数
}

// modelContextLimit 返回给定模型的已知上下文 token 上限（保留约 20% 余量）。
// 未知模型返回保守默认值 32768。
func modelContextLimit(model string) int {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "claude-3-5") || strings.Contains(m, "claude-3.5") ||
		strings.Contains(m, "claude-sonnet") || strings.Contains(m, "claude-opus"):
		return 160_000 // Claude 3.5/3: 200K，留 40K 给响应和 system
	case strings.Contains(m, "claude"):
		return 160_000
	case strings.Contains(m, "gpt-4o") || strings.Contains(m, "gpt-4"):
		return 100_000 // GPT-4o: 128K，留余量
	case strings.Contains(m, "gpt-3.5"):
		return 12_000 // GPT-3.5: 16K
	case strings.Contains(m, "deepseek"):
		return 55_000 // DeepSeek: 64K
	case strings.Contains(m, "minimax") || strings.Contains(m, "m2.5") || strings.Contains(m, "m2"):
		return 800_000 // MiniMax M2: 1M 上下文
	case strings.Contains(m, "qwen"):
		return 100_000 // Qwen: 128K
	case strings.Contains(m, "k2"):
		return 100_000 // Kimi K2: 128K+
	default:
		return 32_768 // 保守默认
	}
}

// getSessionSummary 线程安全地读取会话摘要。
func (a *Agent) getSessionSummary(sessionID string) string {
	a.summaryMu.RLock()
	defer a.summaryMu.RUnlock()
	return a.sessionSummaries[sessionID]
}

// setSessionSummary 线程安全地写入/删除会话摘要。
func (a *Agent) setSessionSummary(sessionID, summary string) {
	a.summaryMu.Lock()
	defer a.summaryMu.Unlock()
	if summary == "" {
		delete(a.sessionSummaries, sessionID)
	} else {
		a.sessionSummaries[sessionID] = summary
	}
}

// resolveProvider 返回当前应使用的 AI Provider、模型名、以及该模型的上下文 token 限制。
// 优先使用当前 session 覆盖的 provider，否则使用默认 provider。
// resolveProvider 返回当前应使用的 AI Provider、模型名、ProviderID 以及上下文限制。
func (a *Agent) resolveProvider(ctx context.Context) (ai.Provider, string, string, int) {
	if a.cfg == nil {
		return a.provider, a.model, "internal-fallback", modelContextLimit(a.model)
	}

	// 1. 检查当前 session 是否有覆盖的 provider（从数据库读取）
	setting, _ := a.store.GetAgentSetting(ctx, a.id)
	if setting != nil {
		log.Printf("[%s] resolveProvider: 从数据库获取到 setting, providerID=%s, model=%s", a.name, setting.ProviderID, setting.Model)
		if p, ok := a.cfg.GetProvider(setting.ProviderID); ok {
			ctxLimit := p.MaxContext
			if ctxLimit <= 0 {
				ctxLimit = modelContextLimit(p.Model)
			}
			if p.APIType == "openai" {
				return ai.NewOpenAIProvider(p.APIKey, p.BaseURL, p.Model, &ai.ProviderOptions{ContextWindow: ctxLimit}), p.Model, p.Name, ctxLimit
			}
			return ai.NewAnthropicProvider(p.APIKey, p.BaseURL, p.Model, &ai.ProviderOptions{ContextWindow: ctxLimit}), p.Model, p.Name, ctxLimit
		}
		log.Printf("[%s] resolveProvider: 未从 cfg 获取到 provider=%s", a.name, setting.ProviderID)
	}

	// 2. 使用默认 provider
	if p, ok := a.cfg.DefaultProvider(); ok {
		ctxLimit := p.MaxContext
		if ctxLimit <= 0 {
			ctxLimit = modelContextLimit(p.Model)
		}
		if p.APIType == "openai" {
			return ai.NewOpenAIProvider(p.APIKey, p.BaseURL, p.Model, &ai.ProviderOptions{ContextWindow: ctxLimit}), p.Model, p.Name, ctxLimit
		}
		return ai.NewAnthropicProvider(p.APIKey, p.BaseURL, p.Model, &ai.ProviderOptions{ContextWindow: ctxLimit}), p.Model, p.Name, ctxLimit
	}

	return a.provider, a.model, "legacy-boot", modelContextLimit(a.model)
}

// HandleMessage 处理传入的消息并返回代理的响应。
// 它运行完整的代理循环（可能会多次调用工具）。
func (a *Agent) HandleMessage(ctx context.Context, sessionID, userMessage string, isMainSession bool) (string, error) {
	// 1. 前置准备与拦截
	if err := a.sem.Acquire(ctx, 1); err != nil {
		return "", fmt.Errorf("代理繁忙: %w", err)
	}
	defer a.sem.Release(1)

	if _, err := a.store.GetOrCreateSession(ctx, sessionID); err != nil {
		return "", fmt.Errorf("获取会话失败: %w", err)
	}

	// 系统指令优先级最高
	if strings.HasPrefix(strings.TrimSpace(userMessage), "/") {
		if resp, intercepted := a.handleSystemCommand(ctx, sessionID, userMessage); intercepted {
			return resp, nil
		}
	}

	// 2. 自动康复与数据加载
	storedMsgs, err := a.store.GetMessages(ctx, sessionID)
	if err == nil && len(storedMsgs) > 0 {
		// 检查最后一条消息是否需要康复
		last := storedMsgs[len(storedMsgs)-1]
		if last.Role == "assistant" && last.Type == "json" {
			log.Printf("[%s] 检测到未完成的工具调用，正在填补中断错误...", a.name)
			var contents []ai.Content
			if err := json.Unmarshal([]byte(last.Content), &contents); err == nil {
				var resultContents []ai.Content
				for _, c := range contents {
					if c.Type == "tool_use" {
						empty := fmt.Sprintf("Error: System interrupted during tool execution (%s).", c.Name)
						resultContents = append(resultContents, ai.Content{
							Type:      "tool_result",
							ToolUseID: c.ID,
							Content:   &empty,
						})
					}
				}
				if len(resultContents) > 0 {
					resultsBytes, _ := json.Marshal(resultContents)
					if err := a.store.AddComplexMessage(ctx, sessionID, "user", string(resultsBytes), "json"); err != nil {
						log.Printf("代理 %s: 填补中断错误失败: %v", a.id, err)
					}
				}
			}
		}

		// 额外检查：检测并清理孤立的 tool_result
		toolUseIDs := make(map[string]bool)
		var orphanedMsgIDs []int64
		for _, m := range storedMsgs {
			if m.Type != "json" {
				continue
			}
			var contents []ai.Content
			if err := json.Unmarshal([]byte(m.Content), &contents); err == nil {
				for _, c := range contents {
					if c.Type == "tool_use" {
						toolUseIDs[c.ID] = true
					}
				}
			}
		}
		for _, m := range storedMsgs {
			if m.Type != "json" || m.Role != "user" {
				continue
			}
			var contents []ai.Content
			if err := json.Unmarshal([]byte(m.Content), &contents); err == nil {
				hasOrphanedResult := false
				for _, c := range contents {
					if c.Type == "tool_result" && !toolUseIDs[c.ToolUseID] {
						hasOrphanedResult = true
						break
					}
				}
				if hasOrphanedResult {
					orphanedMsgIDs = append(orphanedMsgIDs, m.ID)
				}
			}
		}
		if len(orphanedMsgIDs) > 0 {
			a.store.DeleteMessagesByIDs(ctx, orphanedMsgIDs)
			storedMsgs, _ = a.store.GetMessages(ctx, sessionID)
		}
	}

	// 持久化当前用户消息
	if err := a.store.AddMessage(ctx, sessionID, "user", userMessage); err != nil {
		return "", fmt.Errorf("添加用户消息失败: %w", err)
	}

	// 3. 上下文压缩与加载（token 估算驱动 + 模型上下文能力自适应）
	if a.contextWindow > 0 {
		storedMsgs, _ = a.store.GetMessages(ctx, sessionID)
		_, _, _, currentCtxLimit := a.resolveProvider(ctx)
		compressThresholdTokens := int(float64(currentCtxLimit) * 0.90)
		estimatedToks := estimateTokens(storedMsgs)
		if estimatedToks > compressThresholdTokens || len(storedMsgs) > int(float64(a.contextWindow)*1.2) {
			splitIdx := safeCompressSplitIndex(storedMsgs)
			if splitIdx > 0 {
				_, currentModel, _, _ := a.resolveProvider(ctx)
				p, _, _, _ := a.resolveProvider(ctx)
				if err := a.compressHistory(ctx, sessionID, storedMsgs[:splitIdx], p, currentModel); err != nil {
					log.Printf("代理 %s: 压缩历史失败: %v", a.id, err)
				}
			}
		}
	}

	// 获取最终供 AI 消费的消息列表
	storedMsgs, err = a.store.GetMessages(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("获取消息失败: %w", err)
	}

	// 3. 进入决策循环
	execCtx := &tools.ExecContext{
		WorkspaceRoot: a.loader.Root(),
		ProjectRoot:   a.projectRoot,
		SessionID:     sessionID,
		AgentID:       a.id,
		CompactFunc:   a.CompactContext,
	}

	messages := convertMessages(storedMsgs)
	toolDefs := a.registry.Definitions()

	log.Printf("[%s] 会话=%s 用户=%q (历史=%d 条, 工具=%d 个)",
		a.name, sessionID, truncate(userMessage, 80), len(storedMsgs), len(toolDefs))

	// 代理循环：调用 AI，执行工具，重复执行
	const maxIterations = 20
	var finalResponse strings.Builder

	for iter := 0; iter < maxIterations; iter++ {
		// 每次迭代都重新解析提供商和提示词，实现对 switch_model 等工具的实时感知
		p, currentModel, _, currentCtxLimit := a.resolveProvider(ctx)

		// 刷新系统提示词（含 Runtime Status 注入）
		sessionSummary := a.getSessionSummary(sessionID)
		systemPrompt, err := a.loader.SystemPrompt(wslib.RuntimeConfig{
			AgentID:      a.id,
			Model:        currentModel,
			ContextLimit: currentCtxLimit,
			CurrentTime:  time.Now(),
		}, isMainSession, sessionSummary)
		if err != nil {
			log.Printf("代理 %s: 加载或刷新系统提示词失败: %v", a.id, err)
		}

		req := &ai.Request{
			Model:    currentModel,
			System:   systemPrompt,
			Messages: messages,
			Tools:    toolDefs,
		}

		ch, err := p.Stream(ctx, req)
		if err != nil {
			return "", fmt.Errorf("流式调用失败: %w", err)
		}

		var textBuffer strings.Builder
		var thinkingBuffer strings.Builder
		var toolCalls []toolCall
		var stopReason string

		for event := range ch {
			switch event.Type {
			case ai.EventText:
				textBuffer.WriteString(event.Text)
				finalResponse.WriteString(event.Text)
			case ai.EventThinking:
				thinkingBuffer.WriteString(event.Thinking)
				log.Printf("[%s] 思考中: %s", a.name, truncate(event.Thinking, 100))
			case ai.EventToolUse:
				log.Printf("[%s] 工具调用: %s 输入=%s", a.name, event.ToolName, truncate(string(event.ToolInput), 120))
				toolCalls = append(toolCalls, toolCall{
					id:    event.ToolUseID,
					name:  event.ToolName,
					input: event.ToolInput,
				})
			case ai.EventStopReason:
				stopReason = event.StopReason
			case ai.EventError:
				return finalResponse.String(), event.Err
			}
		}

		// 记录 AI 响应
		if txt := textBuffer.String(); txt != "" {
			log.Printf("[%s] 迭代=%d 文本=%q 停止原因=%s", a.name, iter, truncate(txt, 120), stopReason)
		} else if len(toolCalls) > 0 {
			log.Printf("[%s] 迭代=%d %d 个工具调用, 停止原因=%s", a.name, iter, len(toolCalls), stopReason)
		}

		// 持久化助手响应
		if len(toolCalls) > 0 || thinkingBuffer.Len() > 0 {
			// 包含工具调用或思考块：保存为 JSON
			assistantMsg := buildAssistantWithToolsAndThinking(textBuffer.String(), thinkingBuffer.String(), toolCalls)
			contentBytes, _ := json.Marshal(assistantMsg.Content)
			if err := a.store.AddComplexMessage(ctx, sessionID, "assistant", string(contentBytes), "json"); err != nil {
				log.Printf("代理 %s: 持久化助手复杂消息失败: %v", a.id, err)
			}
			messages = append(messages, assistantMsg)
		} else if text := textBuffer.String(); text != "" {
			// 仅文本：保存为文本
			if err := a.store.AddMessage(ctx, sessionID, "assistant", text); err != nil {
				log.Printf("代理 %s: 持久化助手消息失败: %v", a.id, err)
			}
		}

		// 如果有工具调用，则执行（忽略 stopReason —— MiniMax 即使在 tool_use 时也可能发送 end_turn）
		if len(toolCalls) > 0 {
			// (已在上方附加了 assistantMsg)

			// 并发执行所有工具调用
			toolResults := a.executeTools(toolCalls, execCtx)

			// 持久化工具结果
			resultsBytes, _ := json.Marshal(toolResults)
			if err := a.store.AddComplexMessage(ctx, sessionID, "user", string(resultsBytes), "json"); err != nil {
				log.Printf("代理 %s: 持久化工具结果失败: %v", a.id, err)
			}

			// 将工具结果作为用户消息附加
			messages = append(messages, ai.Message{
				Role:    "user",
				Content: toolResults,
			})
			continue
		}

		// 没有工具调用：处理完成
		break
	}

	response := finalResponse.String()
	if response == "" {
		response = "(无响应)"
	}
	log.Printf("[%s] 处理完成 会话=%s 响应=%q", a.name, sessionID, truncate(response, 120))
	return response, nil
}

// safeCompressSplitIndex 找到安全的压缩分割点，确保不截断 tool_use / tool_result 调用对。
// 返回值是"可以压缩到此处（不含）"的安全边界索引。
// 规则：分割点必须是 user 消息，且其前一条不是 assistant tool_use 消息。
func safeCompressSplitIndex(msgs []*store.Message) int {
	// 默认压缩前 60%
	target := int(float64(len(msgs)) * 0.6)
	if target <= 0 {
		return 0
	}
	// 从 target 向前找安全边界
	for i := target; i > 1; i-- {
		m := msgs[i-1]
		if m.Role != "user" {
			continue
		}
		if m.Type == "json" {
			// user 的 json 消息是 tool_result，不能在此切分
			continue
		}
		// 前一条是否是 assistant tool_use？
		prev := msgs[i-2]
		if prev.Role == "assistant" && prev.Type == "json" {
			// 前一条是 tool_use，此处切分会留下孤立的 tool_result，跳过
			continue
		}
		return i
	}
	return 0
}

func (a *Agent) compressHistory(ctx context.Context, sessionID string, toCompress []*store.Message, p ai.Provider, model string) error {
	// 转换待压缩消息为 AI 格式以供摘要
	aiMsgs := convertMessages(toCompress)

	// 准备摘要请求
	summaryPrompt := `你是一个历史记录压缩器。请将以下对话历史压缩成一段简洁的摘要。
要求：
1. 保留关键决策、用户偏好、重要事实和正在进行的任务状态。
2. 使用客观、简练的语言。
3. 长度控制在 300 汉字以内。
4. 格式：[历史背景摘要]: <内容>`

	req := &ai.Request{
		Model:    model,
		System:   "你是一个高效的上下文整理专家。",
		Messages: append(aiMsgs, ai.TextMessage("user", summaryPrompt)),
	}

	ch, err := p.Stream(ctx, req)
	if err != nil {
		return err
	}

	var summary strings.Builder
	for event := range ch {
		switch event.Type {
		case ai.EventText:
			summary.WriteString(event.Text)
		case ai.EventError:
			return event.Err
		}
	}

	summaryContent := strings.TrimSpace(summary.String())
	if summaryContent == "" {
		return fmt.Errorf("生成的摘要为空")
	}

	// 获取所有消息，以便保留未被压缩的部分
	allMsgs, err := a.store.GetMessages(ctx, sessionID)
	if err != nil {
		return err
	}

	// 找出未被压缩的部分 (IDs 大于 toCompress 最后一条的)
	var remaining []*store.Message
	if len(toCompress) > 0 {
		lastID := toCompress[len(toCompress)-1].ID
		for _, m := range allMsgs {
			if m.ID > lastID {
				remaining = append(remaining, m)
			}
		}
	}

	// 删除旧消息（不再重新插入摘要到历史体，避免破坏角色交替）
	if err := a.store.DeleteAllMessages(ctx, sessionID); err != nil {
		return err
	}

	// 摘要存入内存（会话级，注入到 system prompt 末尾）
	// 若已有旧摘要（多轮压缩），追加
	existing := a.getSessionSummary(sessionID)
	var newSummary string
	if existing != "" {
		newSummary = existing + "\n\n" + summaryContent
	} else {
		newSummary = summaryContent
	}
	a.setSessionSummary(sessionID, newSummary)

	// 重新插入剩余消息
	for _, m := range remaining {
		if err := a.store.AddComplexMessage(ctx, sessionID, m.Role, m.Content, m.Type); err != nil {
			log.Printf("恢复剩余消息失败: %v", err)
		}
	}

	log.Printf("[%s] 上下文压缩完成: 删除 %d 条旧消息，保留 %d 条，摘要已注入 system prompt",
		a.name, len(toCompress), len(remaining))
	return nil
}

// CompactContext 是 compressHistory 的公开入口，供 compact_context 工具调用。
// 它在当前时刻（AI 主动调用时，一定处于安全的工具链边界）执行完整的历史压缩。
func (a *Agent) CompactContext(ctx context.Context, sessionID string) error {
	p, currentModel, _, _ := a.resolveProvider(ctx)
	storedMsgs, err := a.store.GetMessages(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("获取消息失败: %w", err)
	}
	if len(storedMsgs) == 0 {
		return fmt.Errorf("当前会话没有可压缩的消息")
	}
	// 压缩除最后 5 条（当前对话轮次）之外的所有消息
	splitIdx := safeCompressSplitIndex(storedMsgs)
	if splitIdx <= 0 {
		return fmt.Errorf("当前消息不足，无需压缩")
	}
	return a.compressHistory(ctx, sessionID, storedMsgs[:splitIdx], p, currentModel)
}

// HandleMessageAsync 在 goroutine 中运行 HandleMessage 并返回一个渠道。
func (a *Agent) HandleMessageAsync(ctx context.Context, sessionID, userMessage string, isMainSession bool) <-chan Result {
	ch := make(chan Result, 1)
	go func() {
		defer close(ch)
		text, err := a.HandleMessage(ctx, sessionID, userMessage, isMainSession)
		ch <- Result{Text: text, Err: err}
	}()
	return ch
}

// Result 保存异步处理的结果。
type Result struct {
	Text string
	Err  error
}

// --- 工具执行 ---

type toolCall struct {
	id    string
	name  string
	input json.RawMessage
}

func (a *Agent) executeTools(calls []toolCall, ctx *tools.ExecContext) []ai.Content {
	results := make([]ai.Content, len(calls))
	var wg sync.WaitGroup

	for i, tc := range calls {
		i, tc := i, tc
		wg.Add(1)
		go func() {
			defer wg.Done()

			tool, ok := a.registry.Get(tc.name)
			var output string
			if !ok {
				output = fmt.Sprintf("错误: 未知工具 %q", tc.name)
			} else {
				result, err := tool.Execute(tc.input, ctx)
				if err != nil {
					output = fmt.Sprintf("错误: %s", err.Error())
					log.Printf("工具 %s 错误: %v", tc.name, err)
				} else {
					output = result
				}
			}

			results[i] = ai.Content{
				Type:      "tool_result",
				ToolUseID: tc.id,
				Content:   &output,
			}
		}()
	}

	wg.Wait()
	return results
}

// --- 助手函数 ---

func convertMessages(stored []*store.Message) []ai.Message {
	var msgs []ai.Message
	for _, m := range stored {
		var newMsg ai.Message

		if m.Type == "json" {
			var content []ai.Content
			if err := json.Unmarshal([]byte(m.Content), &content); err == nil {
				newMsg = ai.Message{Role: m.Role, Content: content}
			} else {
				newMsg = ai.TextMessage(m.Role, m.Content)
			}
		} else {
			newMsg = ai.TextMessage(m.Role, m.Content)
		}

		// 合并逻辑：如果当前消息角色与上一条消息角色相同，则合并内容
		if len(msgs) > 0 {
			lastIdx := len(msgs) - 1
			if msgs[lastIdx].Role == newMsg.Role {
				msgs[lastIdx].Content = append(msgs[lastIdx].Content, newMsg.Content...)
				continue
			}
		}

		msgs = append(msgs, newMsg)
	}

	log.Printf("convertMessages: 原始消息 %d 条，转换后 %d 条", len(stored), len(msgs))

	// Anthropic API 及其兼容版本 (如 Minimax) 要求：
	// 1. 消息列表必须以 "user" 角色开始。
	// 2. 角色必须交替出现 (已通过上方的合并逻辑保证)。
	for len(msgs) > 0 && msgs[0].Role != "user" {
		msgs = msgs[1:]
	}

	// --- 孤立工具链顺序清理 ---
	// 修复由于压缩或头部对齐引起的 "invalid tool result: tool id not found" 错误。
	//
	// 策略：两步验证
	//   第 1 步：集合预过滤——收集所有 tool_use IDs 和 tool_result IDs，
	//            过滤掉在整个历史中根本没有配对的孤立块。
	//   第 2 步：顺序验证——按消息顺序重新扫描，确保每个 tool_result 的对应
	//            tool_use 确实出现在其之前（而不是之后），避免顺序倒置导致 API 400。

	// 第 1 步：预过滤——收集所有 tool_use IDs 和 tool_result IDs
	allToolUseIDs := make(map[string]bool)
	allToolResultIDs := make(map[string]bool)
	for _, msg := range msgs {
		for _, block := range msg.Content {
			switch block.Type {
			case "tool_use":
				allToolUseIDs[block.ID] = true
			case "tool_result":
				allToolResultIDs[block.ToolUseID] = true
			}
		}
	}

	// 基于集合预过滤（只保留双向都有配对的块）
	var preMsgs []ai.Message
	for _, msg := range msgs {
		var cleanContent []ai.Content
		for _, block := range msg.Content {
			switch block.Type {
			case "tool_use":
				if allToolResultIDs[block.ID] {
					cleanContent = append(cleanContent, block)
				} else {
					log.Printf("convertMessages: 预过滤孤立 tool_use（无对应 tool_result）[ID=%s name=%s]", block.ID, block.Name)
				}
			case "tool_result":
				if allToolUseIDs[block.ToolUseID] {
					cleanContent = append(cleanContent, block)
				} else {
					log.Printf("convertMessages: 预过滤孤立 tool_result（无对应 tool_use）[ID=%s]", block.ToolUseID)
				}
			default:
				cleanContent = append(cleanContent, block)
			}
		}
		if len(cleanContent) > 0 {
			msg.Content = cleanContent
			preMsgs = append(preMsgs, msg)
		}
	}

	// 第 2 步：顺序验证——确保每个 tool_result 的对应 tool_use 出现在其之前
	// 按消息顺序扫描，维护"已见到的 tool_use ID"集合
	seenToolUseIDs := make(map[string]bool)
	var finalMsgs []ai.Message
	for _, msg := range preMsgs {
		var cleanContent []ai.Content
		for _, block := range msg.Content {
			switch block.Type {
			case "tool_use":
				seenToolUseIDs[block.ID] = true
				cleanContent = append(cleanContent, block)
			case "tool_result":
				if seenToolUseIDs[block.ToolUseID] {
					cleanContent = append(cleanContent, block)
				} else {
					// 对应的 tool_use 出现在 tool_result 之后（顺序倒置），或已被清理掉
					log.Printf("convertMessages: 顺序过滤孤立 tool_result（tool_use 未在其之前出现）[ID=%s]", block.ToolUseID)
				}
			default:
				cleanContent = append(cleanContent, block)
			}
		}
		if len(cleanContent) > 0 {
			msg.Content = cleanContent
			finalMsgs = append(finalMsgs, msg)
		}
	}

	log.Printf("convertMessages: 工具链过滤后 %d 条消息", len(finalMsgs))

	// 过滤后重新对齐：合并相邻同角色消息，确保以 user 开始
	if len(finalMsgs) > 0 {
		var aligned []ai.Message
		for i := 0; i < len(finalMsgs); i++ {
			if len(aligned) > 0 && aligned[len(aligned)-1].Role == finalMsgs[i].Role {
				aligned[len(aligned)-1].Content = append(aligned[len(aligned)-1].Content, finalMsgs[i].Content...)
			} else {
				aligned = append(aligned, finalMsgs[i])
			}
		}
		for len(aligned) > 0 && aligned[0].Role != "user" {
			aligned = aligned[1:]
		}
		log.Printf("convertMessages: 对齐后 %d 条消息", len(aligned))
		// 最终再做一次顺序验证（合并可能创造出新的顺序问题）
		result := finalOrderCheck(aligned)
		log.Printf("convertMessages: 最终返回 %d 条消息", len(result))
		return result
	}

	log.Printf("convertMessages: 最终返回 %d 条消息", len(finalMsgs))
	return finalMsgs
}

// finalOrderCheck 对已对齐的消息做最终顺序一致性校验。
// 确保合并相邻同角色消息后，不会出现 tool_result 排在对应 tool_use 之前的情况。
func finalOrderCheck(msgs []ai.Message) []ai.Message {
	seenUseIDs := make(map[string]bool)
	var result []ai.Message
	for _, msg := range msgs {
		var clean []ai.Content
		for _, block := range msg.Content {
			switch block.Type {
			case "tool_use":
				seenUseIDs[block.ID] = true
				clean = append(clean, block)
			case "tool_result":
				if seenUseIDs[block.ToolUseID] {
					clean = append(clean, block)
				} else {
					log.Printf("convertMessages: 最终顺序校验过滤 tool_result [ID=%s]", block.ToolUseID)
				}
			default:
				clean = append(clean, block)
			}
		}
		if len(clean) > 0 {
			msg.Content = clean
			result = append(result, msg)
		}
	}
	return result
}

func buildAssistantWithToolsAndThinking(text, thinking string, calls []toolCall) ai.Message {
	var contents []ai.Content
	if thinking != "" {
		contents = append(contents, ai.Content{Type: "thinking", Thinking: thinking})
	}
	if text != "" {
		contents = append(contents, ai.Content{Type: "text", Text: text})
	}
	for _, tc := range calls {
		contents = append(contents, ai.Content{
			Type:  "tool_use",
			ID:    tc.id,
			Name:  tc.name,
			Input: tc.input,
		})
	}
	return ai.Message{Role: "assistant", Content: contents}
}

// Manager 管理多个代理。
type Manager struct {
	agents map[string]*Agent
	mu     sync.RWMutex
}

func NewManager() *Manager {
	return &Manager{agents: make(map[string]*Agent)}
}

func (m *Manager) Register(a *Agent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agents[a.id] = a
}

// GetForChannel 返回配置为处理给定渠道的所有代理。
func (m *Manager) GetForChannel(channelName string, agentChannels map[string][]string) []*Agent {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*Agent
	for id, channels := range agentChannels {
		for _, c := range channels {
			if c == channelName {
				if a, ok := m.agents[id]; ok {
					result = append(result, a)
				}
				break
			}
		}
	}
	return result
}

func (m *Manager) Get(id string) (*Agent, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.agents[id]
	return a, ok
}

// CleanupOrphanedToolMessages 扫描所有会话，清理孤立的工具消息
// 启动时调用，确保所有会话历史都是健康的
func (m *Manager) CleanupOrphanedToolMessages(ctx context.Context, store store.Store) error {
	sessions, err := store.ListSessions(ctx)
	if err != nil {
		return fmt.Errorf("列出会话失败: %w", err)
	}

	totalCleaned := 0
	for _, sess := range sessions {
		msgs, err := store.GetMessages(ctx, sess.ID)
		if err != nil {
			log.Printf("[Cleanup] 获取会话 %s 消息失败: %v", sess.ID, err)
			continue
		}

		// 收集所有 tool_use IDs 和 tool_result IDs
		toolUseIDs := make(map[string]bool)
		toolResultIDs := make(map[string]bool)
		var orphanedIDs []int64

		for _, m := range msgs {
			if m.Type != "json" {
				continue
			}
			var contents []ai.Content
			if err := json.Unmarshal([]byte(m.Content), &contents); err != nil {
				continue
			}
			for _, c := range contents {
				switch c.Type {
				case "tool_use":
					toolUseIDs[c.ID] = true
				case "tool_result":
					toolResultIDs[c.ToolUseID] = true
				}
			}
		}

		// 找出孤立消息：有 tool_result 但没有对应 tool_use
		for _, m := range msgs {
			if m.Type != "json" || m.Role != "user" {
				continue
			}
			var contents []ai.Content
			if err := json.Unmarshal([]byte(m.Content), &contents); err != nil {
				continue
			}
			isOrphaned := false
			for _, c := range contents {
				if c.Type == "tool_result" && !toolUseIDs[c.ToolUseID] {
					isOrphaned = true
					break
				}
			}
			if isOrphaned {
				orphanedIDs = append(orphanedIDs, m.ID)
			}
		}

		// 删除孤立消息
		if len(orphanedIDs) > 0 {
			if err := store.DeleteMessagesByIDs(ctx, orphanedIDs); err != nil {
				log.Printf("[Cleanup] 删除会话 %s 孤立消息失败: %v", sess.ID, err)
			} else {
				log.Printf("[Cleanup] 会话 %s: 清理了 %d 条孤立工具消息", sess.ID, len(orphanedIDs))
				totalCleaned += len(orphanedIDs)
			}
		}
	}

	if totalCleaned > 0 {
		log.Printf("[Cleanup] 总计清理了 %d 条孤立工具消息", totalCleaned)
	} else {
		log.Printf("[Cleanup] 未发现孤立工具消息，所有会话健康")
	}
	return nil
}

// StartTime 记录进程何时启动（用于健康检查）。
var StartTime = time.Now()

// handleSystemCommand 处理以 / 开头的硬拦截指令。
func (a *Agent) handleSystemCommand(ctx context.Context, sessionID, msg string) (string, bool) {
	fields := strings.Fields(msg)
	if len(fields) == 0 {
		return "", false
	}
	cmd := strings.ToLower(fields[0])

	switch cmd {
	case "/clear":
		log.Printf("[%s] 系统指令拦截: 清空上下文", a.name)
		if err := a.store.DeleteAllMessages(ctx, sessionID); err != nil {
			return fmt.Sprintf("❌ 清空失败: %v", err), true
		}
		// 同时清除内存中的历史摘要
		a.setSessionSummary(sessionID, "")
		return "🧹 上下文已清空，历史摘要已重置。对话已重置。", true

	case "/status":
		return GetStatusSummary(a, ctx, sessionID), true

	case "/model":
		if len(fields) == 1 {
			_, currentModel, _, _ := a.resolveProvider(ctx)
			return fmt.Sprintf("🤖 当前使用的模型为: **%s**。您可以使用 `/status` 查看完整配置，或使用 `/model <provider>` 切换。", currentModel), true
		}
		// /model <provider> [model_override] —— 切换供应商
		providerName := fields[1]
		if a.cfg == nil {
			return "❌ 配置未加载", true
		}
		p, ok := a.cfg.GetProvider(providerName)
		if !ok {
			return fmt.Sprintf("❌ 未找到供应商 %q。\n可用指令：/providers 查看列表，修改 .env 后重启添加新供应商。", providerName), true
		}
		targetModel := p.Model
		if len(fields) >= 3 {
			targetModel = fields[2] // 可选覆盖具体模型名
		}
		if err := a.store.SetAgentModel(ctx, a.id, targetModel, providerName); err != nil {
			return fmt.Sprintf("❌ 切换失败: %v", err), true
		}
		log.Printf("[%s] 模型已切换: provider=%s model=%s", a.name, providerName, targetModel)
		// 显示上下文能力
		shownLimit := p.MaxContext
		if shownLimit <= 0 {
			shownLimit = modelContextLimit(targetModel)
		}
		return fmt.Sprintf("✅ 已切换到 **%s**（模型: %s，上下文: ~%dk tokens）。下条消息即刻生效。",
			providerName, targetModel, shownLimit/1000), true

	case "/providers":
		if a.cfg == nil {
			return "❌ 配置未加载", true
		}
		providers := a.cfg.ListProviders()
		if len(providers) == 0 {
			return "📭 暂无已配置的供应商。\n请修改 .env 文件添加 PROVIDER_XXX_API_KEY 后重启。", true
		}
		_, currentModel, _, _ := a.resolveProvider(ctx)
		var sb strings.Builder
		defaultProvider, _ := a.cfg.DefaultProvider()
		sb.WriteString("📋 已配置的模型供应商：\n\n")
		for _, p := range providers {
			marker := ""
			if defaultProvider != nil && p.Name == defaultProvider.Name {
				marker = "  🌟 默认"
			}
			if p.Model == currentModel {
				marker = "  ✅ 当前"
			}
			ctxLimit := p.MaxContext
			if ctxLimit <= 0 {
				ctxLimit = modelContextLimit(p.Model)
			}
			sb.WriteString(fmt.Sprintf("• **%s**%s\n  模型: %s | 协议: %s | 上下文: ~%dk\n\n",
				p.Name, marker, p.Model, p.APIType, ctxLimit/1000))
		}
		sb.WriteString("切换用法：/model <供应商名>  或  /model <供应商名> <具体模型名>")
		return sb.String(), true

	case "/help":
		return "🛠️ **系统指令帮助**\n\n" +
			"🔸 `/status` — 查看机器人身份、模型、供应商及健康度汇总\n" +
			"🔸 `/providers` — 列出所有已配置的供应商及其能力\n" +
			"🔸 `/model <p>` — 切换供应商（示例：`/model minimax`）\n" +
			"🔸 `/clear` — 清空当前会话历史和摘要（API 报错自救）\n" +
			"🔸 `/help` — 显示本帮助\n\n" +
			"⚠️ **注意**: 机器人（AI）本身仅具备这些指令的查询建议权，不具备自动执行权限。所有指令必须由用户手动输入执行。", true
	}
	return "", false
}
