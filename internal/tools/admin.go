package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"goclaw/internal/ai"
	"goclaw/internal/store"
	"strings"
	"time"
)

// AddProviderTool 允许动态增加 AI 供应商。
// 注意：现在 provider 通过 .env 文件配置，此工具仅用于提示用户。
type AddProviderTool struct{}

func (t *AddProviderTool) Name() string { return "add_provider" }
func (t *AddProviderTool) Description() string {
	return `添加新的 AI 模型供应商配置。

⚠️ 重要：当前版本 provider 通过 .env 文件配置，此工具不再直接修改配置。
请按以下步骤添加新供应商：

1. 编辑 .env 文件，添加如下配置：
   PROVIDER_<名称>_API_KEY=your_api_key
   PROVIDER_<名称>_BASE_URL=https://api.xxx.com
   PROVIDER_<名称>_MODEL=model-name
   PROVIDER_<名称>_TYPE=anthropic  # 或 openai
   PROVIDER_<名称>_CONTEXT=128000   # 可选，上下文窗口大小

2. 重启程序使配置生效

示例（添加 DeepSeek）：
   PROVIDER_DEEPSEEK_API_KEY=sk-xxxxx
   PROVIDER_DEEPSEEK_BASE_URL=https://api.deepseek.com
   PROVIDER_DEEPSEEK_MODEL=deepseek-chat
   PROVIDER_DEEPSEEK_TYPE=openai`
}

func (t *AddProviderTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}

func (t *AddProviderTool) Execute(input json.RawMessage, ctx *ExecContext) (string, error) {
	return t.Description(), nil
}

// DeleteProviderTool 允许删除已配置的 AI 供应商。
// 注意：现在 provider 通过 .env 文件配置，此工具仅用于提示用户。
type DeleteProviderTool struct{}

func (t *DeleteProviderTool) Name() string { return "delete_provider" }
func (t *DeleteProviderTool) Description() string {
	return `删除已配置的 AI 模型供应商。

⚠️ 重要：当前版本 provider 通过 .env 文件配置，请直接编辑 .env 文件删除相关 PROVIDER_XXX_* 变量，然后重启程序。`
}
func (t *DeleteProviderTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}

func (t *DeleteProviderTool) Execute(input json.RawMessage, ctx *ExecContext) (string, error) {
	return t.Description(), nil
}

// checkConnectivity 执行简单的 API 调用以验证配置
func checkConnectivity(apiKey, baseURL, model, apiType string) error {
	var provider ai.Provider
	if apiType == "openai" {
		provider = ai.NewOpenAIProvider(apiKey, baseURL, model, nil)
	} else {
		provider = ai.NewAnthropicProvider(apiKey, baseURL, model, nil)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := &ai.Request{
		Model:     model,
		Messages:  []ai.Message{ai.TextMessage("user", "Ping. Just say 'pong'.")},
		MaxTokens: 10,
	}

	ch, err := provider.Stream(ctx, req)
	if err != nil {
		return err
	}

	// 只要能从流中读取到任何非错误事件，就认为连接成功
	for event := range ch {
		if event.Type == ai.EventError {
			return event.Err
		}
		if event.Type == ai.EventText || event.Type == ai.EventStopReason || event.Type == ai.EventDone {
			return nil
		}
	}
	return fmt.Errorf("未收到有效响应")
}

// SwitchModelTool 允许切换代理使用的模型。
type SwitchModelTool struct {
	Store store.Store
}

func (t *SwitchModelTool) Name() string { return "switch_model" }
func (t *SwitchModelTool) Description() string {
	return `为指定的代理切换模型供应商。

⚠️ 注意：provider 必须通过 .env 文件预先配置。使用此工具前，请确保已添加对应的 PROVIDER_XXX_API_KEY。`
}
func (t *SwitchModelTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"agent_id": {"type": "string", "description": "代理 ID，例如 'main'"},
			"provider_name": {"type": "string", "description": "目标供应商名称（必须在 .env 中配置）"},
			"model": {"type": "string", "description": "可选：覆盖供应商的默认模型"}
		},
		"required": ["agent_id", "provider_name"]
	}`)
}

func (t *SwitchModelTool) Execute(input json.RawMessage, ctx *ExecContext) (string, error) {
	var in struct {
		AgentID      string `json:"agent_id"`
		ProviderName string `json:"provider_name"`
		Model        string `json:"model"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}

	targetModel := in.Model
	if targetModel == "" {
		// 尝试从 .env 获取默认模型（如果 ExecContext 有 Config 引用）
		targetModel = "default" // 将由 agent 在运行时解析
	}

	// 保存设置到数据库（仅记录覆盖，provider 配置来自 ENV）
	err := t.Store.SetAgentModel(context.Background(), in.AgentID, targetModel, in.ProviderName)
	if err != nil {
		return "", fmt.Errorf("无法更新代理设置: %w", err)
	}

	return fmt.Sprintf("🚀 成功！代理 %q 将使用供应商 %q (模型: %s)。请确保该 provider 已在 .env 中配置。", in.AgentID, in.ProviderName, targetModel), nil
}

// ListAgentsTool 列出所有代理及其当前配置。
type ListAgentsTool struct {
	Store store.Store
}

func (t *ListAgentsTool) Name() string { return "list_agents" }
func (t *ListAgentsTool) Description() string {
	return "列出当前系统中所有的代理。provider 列表需通过 /providers 指令查看。"
}
func (t *ListAgentsTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}

func (t *ListAgentsTool) Execute(input json.RawMessage, ctx *ExecContext) (string, error) {
	agents, _ := t.Store.ListDataAgents(context.Background())

	var sb strings.Builder
	sb.WriteString("### 已配置代理列表:\n")
	if len(agents) == 0 {
		sb.WriteString("- (无动态创建的代理)\n")
	}
	for _, a := range agents {
		sb.WriteString(fmt.Sprintf("- **%s** (%s): 提供商=%s, 模型=%s, 渠道=[%s]\n", a.Name, a.ID, a.ProviderID, a.Model, a.Channels))
	}

	return sb.String(), nil
}

// CreateAgentTool 允许动态创建一个新的代理。
type CreateAgentTool struct {
	Store store.Store
}

func (t *CreateAgentTool) Name() string { return "create_agent" }
func (t *CreateAgentTool) Description() string {
	return "在系统中动态创建一个新的 AI 代理。创建后需重启程序以完全激活消息路由。"
}
func (t *CreateAgentTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"id": {"type": "string", "description": "唯一 ID，如 'coder'"},
			"name": {"type": "string", "description": "易读名称，如 '资深程序员'"},
			"workspace": {"type": "string", "description": "工作区相对路径，如 'coder_ws'"},
			"provider_id": {"type": "string", "description": "使用的模型提供商 ID"},
			"model": {"type": "string", "description": "具体模型名称"},
			"channels": {"type": "string", "description": "监听的渠道名，逗号分隔，如 'telegram'"}
		},
		"required": ["id", "name", "workspace", "provider_id", "model", "channels"]
	}`)
}

func (t *CreateAgentTool) Execute(input json.RawMessage, ctx *ExecContext) (string, error) {
	var in struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Workspace  string `json:"workspace"`
		ProviderID string `json:"provider_id"`
		Model      string `json:"model"`
		Channels   string `json:"channels"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}

	err := t.Store.CreateAgent(context.Background(), in.ID, in.Name, in.Workspace, in.Channels, in.Model, in.ProviderID)
	if err != nil {
		return "", fmt.Errorf("创建代理失败: %w", err)
	}

	return fmt.Sprintf("✨ 代理 %q (%s) 已创建。请注意：为了激活渠道监听，您可能需要重启程序。", in.Name, in.ID), nil
}

// ClearContextTool 允许代理清空当前会话的上下文（历史记录）。
type ClearContextTool struct {
	Store store.Store
}

func (t *ClearContextTool) Name() string { return "clear_context" }
func (t *ClearContextTool) Description() string {
	return "清空当前会话的上下文历史记录。当对话变得过于冗长、混乱或用户明确要求'重新开始'时使用。注意：这将永久删除此会话的所有聊天历史。"
}
func (t *ClearContextTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"confirm": {"type": "boolean", "description": "确认是否执行清理 (true)"}
		},
		"required": ["confirm"]
	}`)
}

func (t *ClearContextTool) Execute(input json.RawMessage, ctx *ExecContext) (string, error) {
	var in struct {
		Confirm bool `json:"confirm"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}

	if !in.Confirm {
		return "⚠️ 操作已取消。未提供确认。", nil
	}

	err := t.Store.DeleteAllMessages(context.Background(), ctx.SessionID)
	if err != nil {
		return "", fmt.Errorf("清空上下文失败: %w", err)
	}

	return "🧹 已成功清空当前会话的上下文历史记录。新消息将从头开始。", nil
}

// CompactContextTool 让 AI 可以主动将当前会话的历史上下文压缩为摘要。
// 压缩时保留关键信息，不会真正删除记忆。
type CompactContextTool struct{}

func (t *CompactContextTool) Name() string { return "compact_context" }
func (t *CompactContextTool) Description() string {
	return `主动压缩当前会话的对话历史，将旧消息归纳为摘要注入 system prompt，释放上下文空间。

【何时使用】
- 感觉对话历史变得很长，接近上下文容量
- 完成了一个阶段性任务，旧的工具调用记录不再需要保留完整细节
- 用户没有要求清除记忆，但需要"轻装上阵"继续后续任务

【与 clear_context 的区别】
- compact_context: 保留摘要，核心信息不丢失 ✅ 推荐
- clear_context: 彻底清除，从零开始（仅在 API 报错自救时使用）

【注意】压缩后本次对话的关键决策、用户偏好等信息会被总结并继续可用。`
}
func (t *CompactContextTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}

func (t *CompactContextTool) Execute(input json.RawMessage, ctx *ExecContext) (string, error) {
	if ctx.CompactFunc == nil {
		return "", fmt.Errorf("compact_context 功能未在此上下文中启用")
	}
	if err := ctx.CompactFunc(context.Background(), ctx.SessionID); err != nil {
		return "", fmt.Errorf("压缩失败: %w", err)
	}
	return "✅ 上下文已压缩。旧消息已归纳为摘要，核心信息已保留。", nil
}
