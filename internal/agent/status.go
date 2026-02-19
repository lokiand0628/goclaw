package agent

import (
	"context"
	"fmt"
	"time"
)

// GetStatusSummary 生成代理当前运行状态的完整汇总字符串。
// 此函数被 handleSystemCommand (/status) 和 SystemStatusTool (AI 查询) 共享。
func GetStatusSummary(a *Agent, ctx context.Context, sessionID string) string {
	_, currentModel, providerID, ctxLimit := a.resolveProvider(ctx)
	summary := a.getSessionSummary(sessionID)
	summaryStatus := "无"
	if summary != "" {
		summaryStatus = fmt.Sprintf("已激活 (%d 字节)", len(summary))
	}

	uptime := time.Since(StartTime).Round(time.Second)

	return fmt.Sprintf(
		"📊 **系统运行状态**\n"+
			"───────────────────\n"+
			"👤 **代理身份**: %s (ID: %s)\n"+
			"🤖 **当前模型**: %s\n"+
			"🏢 **供应商**: %s\n"+
			"🧠 **上下文限制**: ~%dk tokens\n"+
			"📝 **历史摘要**: %s\n"+
			"⏱️ **运行时间**: %v\n"+
			"───────────────────\n"+
			"使用 `/help` 查看更多指令。",
		a.name, a.id, currentModel, providerID, ctxLimit/1000, summaryStatus, uptime,
	)
}

// truncateStr 是包内共享的字符串截断辅助函数
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

// MultiAgentGetter 适配 tools.AgentGetter 接口，支持多代理查找。
type MultiAgentGetter struct {
	Manager *Manager
}

// GetStatus 实现 tools.AgentGetter 接口
func (m *MultiAgentGetter) GetStatus(ctx context.Context, agentID, sessionID string) string {
	// 尝试通过 ID 获取代理
	a, ok := m.Manager.Get(agentID)
	if !ok {
		// 如果未找到（例如 "main" agent ID 可能不匹配），尝试作为备用或直接返回错误
		// 这里假设如果没传 agentID 或者找不到，可能是通过 main 调用的旧逻辑，
		// 但更安全的是报错或尝试找 "main"。
		if agentID == "" {
			a, ok = m.Manager.Get("main")
		}
	}

	if !ok || a == nil {
		return fmt.Sprintf("无法获取系统状态：未找到 ID 为 %q 的代理实例", agentID)
	}

	return GetStatusSummary(a, ctx, sessionID)
}
