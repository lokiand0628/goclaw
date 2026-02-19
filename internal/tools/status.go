package tools

import (
	"context"
	"encoding/json"
)

// AgentGetter 是一个接口，允许工具获取与其关联的代理的状态。
// 这用于打破 tools 和 agent 包之间的循环依赖。
// AgentGetter 是一个接口，允许工具获取与其关联的代理的状态。
// 这用于打破 tools 和 agent 包之间的循环依赖。
type AgentGetter interface {
	GetStatus(ctx context.Context, agentID, sessionID string) string
}

// SystemStatusTool 允许代理获取其当前的状态汇总。
type SystemStatusTool struct {
	Getter AgentGetter
}

func (t *SystemStatusTool) Name() string { return "get_system_status" }

func (t *SystemStatusTool) Description() string {
	return "查询我当前的系统运行状态，包括模型名称、供应商信息、上下文限制和运行时间。当用户询问我的身份或环境但我无法确定时，请使用此工具。"
}

func (t *SystemStatusTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}

func (t *SystemStatusTool) Execute(input json.RawMessage, ctx *ExecContext) (string, error) {
	if t.Getter == nil {
		return "无法获取系统状态：未配置代理获取器", nil
	}
	// 传递 AgentID 以便 Getter 能够找到正确的代理
	return t.Getter.GetStatus(context.Background(), ctx.AgentID, ctx.SessionID), nil
}
