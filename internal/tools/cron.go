package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"goclaw/internal/store"
)

// CronSchedulerIface 是 CronScheduler 提供给工具层的接口，避免循环依赖。
type CronSchedulerIface interface {
	AddJob(ctx context.Context, job *store.CronJob) error
	RemoveJob(jobID string)
	SaveJob(job *store.CronJob) error
	DeleteJob(id string) error
	ListJobs() ([]*store.CronJob, error)
}

// ManageCronTool 让 AI 可以管理定时任务（cron jobs）。
type ManageCronTool struct {
	Store store.Store
	// Sched 在 CronScheduler 启动后被设置，用于动态增删任务。
	Sched CronSchedulerIface
}

func (t *ManageCronTool) Name() string { return "manage_cron" }
func (t *ManageCronTool) Description() string {
	return `管理 AI 代理的定时任务（Cron Jobs）。支持创建、列出、删除、启用/禁用任务。

【Cron 表达式格式】（5 字段：分 时 日 月 周）
  "0 9 * * 1"     → 每周一 09:00
  "30 8 * * 1-5"  → 工作日 08:30
  "0 8 * * *"     → 每天 08:00
  "0 */2 * * *"   → 每 2 小时
  "@every 30m"    → 每 30 分钟
  "@daily"        → 每天午夜
  "@weekly"       → 每周日午夜

【channel_id】可选。填写 Telegram chat ID（如 "1498698446"）则任务响应会发到该 chat；留空则仅记录日志。

【操作类型】action 参数：
  "list"    — 列出所有任务
  "add"     — 创建或更新任务（需要 id, name, cron_expr, prompt）
  "delete"  — 删除任务（需要 id）
  "enable"  — 启用任务（需要 id）
  "disable" — 禁用任务（需要 id）`
}

func (t *ManageCronTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action":     {"type": "string", "enum": ["list","add","delete","enable","disable"], "description": "操作类型"},
			"id":         {"type": "string", "description": "任务唯一 ID（短名，如 'morning-brief'）"},
			"agent_id":   {"type": "string", "description": "执行任务的代理 ID（默认 'main'）"},
			"name":       {"type": "string", "description": "任务的人可读名称"},
			"cron_expr":  {"type": "string", "description": "Cron 表达式，如 '0 9 * * 1'"},
			"prompt":     {"type": "string", "description": "触发时发送给代理的提示词"},
			"channel_id": {"type": "string", "description": "响应发到哪个渠道 chat ID（可选，空表示仅记录日志）"}
		},
		"required": ["action"]
	}`)
}

func (t *ManageCronTool) Execute(input json.RawMessage, ctx *ExecContext) (string, error) {
	var in struct {
		Action    string `json:"action"`
		ID        string `json:"id"`
		AgentID   string `json:"agent_id"`
		Name      string `json:"name"`
		CronExpr  string `json:"cron_expr"`
		Prompt    string `json:"prompt"`
		ChannelID string `json:"channel_id"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", err
	}

	bg := context.Background()

	switch in.Action {
	case "list":
		if t.Sched == nil {
			return "", fmt.Errorf("定时调度器未就绪")
		}
		jobs, err := t.Sched.ListJobs()
		if err != nil {
			return "", fmt.Errorf("查询任务失败: %w", err)
		}
		if len(jobs) == 0 {
			return "📭 暂无定时任务。\n用 action=add 来创建。", nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("📋 当前共 %d 个定时任务：\n\n", len(jobs)))
		for _, j := range jobs {
			status := "✅ 启用"
			if !j.Enabled {
				status = "⏸️ 禁用"
			}
			ch := j.ChannelID
			if ch == "" {
				ch = "（仅日志）"
			}
			sb.WriteString(fmt.Sprintf("• **%s** [%s] %s\n  代理: %s | 表达式: `%s` | 发送到: %s\n  提示词: %s\n\n",
				j.Name, j.ID, status, j.AgentID, j.CronExpr, ch, truncateCron(j.Prompt, 60)))
		}
		return sb.String(), nil

	case "add":
		if in.ID == "" || in.CronExpr == "" || in.Prompt == "" {
			return "", fmt.Errorf("add 操作需要提供 id、cron_expr 和 prompt")
		}
		if in.Name == "" {
			in.Name = in.ID
		}
		if in.AgentID == "" {
			in.AgentID = "main"
		}
		job := &store.CronJob{
			ID:        in.ID,
			AgentID:   in.AgentID,
			Name:      in.Name,
			CronExpr:  in.CronExpr,
			Prompt:    in.Prompt,
			ChannelID: in.ChannelID,
			Enabled:   true,
		}
		if t.Sched == nil {
			return "", fmt.Errorf("定时调度器未就绪")
		}
		if err := t.Sched.SaveJob(job); err != nil {
			return "", fmt.Errorf("保存任务失败: %w", err)
		}
		if err := t.Sched.AddJob(bg, job); err != nil {
			log.Printf("[cron] 动态添加任务失败: %v", err)
		}
		chDesc := in.ChannelID
		if chDesc == "" {
			chDesc = "仅记录日志"
		}
		return fmt.Sprintf("✅ Cron 任务 **%s** (%s) 已创建/更新。\n表达式: `%s` | 发送到: %s",
			in.Name, in.ID, in.CronExpr, chDesc), nil

	case "delete":
		if in.ID == "" {
			return "", fmt.Errorf("delete 操作 need providing id")
		}
		if t.Sched == nil {
			return "", fmt.Errorf("定时调度器未就绪")
		}
		if err := t.Sched.DeleteJob(in.ID); err != nil {
			return "", fmt.Errorf("删除任务失败: %w", err)
		}
		t.Sched.RemoveJob(in.ID)
		return fmt.Sprintf("🗑️ Cron 任务 **%s** 已删除。", in.ID), nil

	case "enable", "disable":
		if in.ID == "" {
			return "", fmt.Errorf("%s 操作 need providing id", in.Action)
		}
		if t.Sched == nil {
			return "", fmt.Errorf("定时调度器未就绪")
		}
		enabled := in.Action == "enable"

		jobs, _ := t.Sched.ListJobs()
		var targetJob *store.CronJob
		for _, j := range jobs {
			if j.ID == in.ID {
				j.Enabled = enabled
				targetJob = j
				break
			}
		}

		if targetJob == nil {
			return "", fmt.Errorf("未找到任务: %s", in.ID)
		}

		if err := t.Sched.SaveJob(targetJob); err != nil {
			return "", fmt.Errorf("更新任务状态失败: %w", err)
		}
		if err := t.Sched.AddJob(bg, targetJob); err != nil {
			log.Printf("[cron] 动态更新任务失败: %v", err)
		}
		verb := "已启用"
		if !enabled {
			verb = "已禁用"
		}
		return fmt.Sprintf("✅ Cron 任务 **%s** %s。", in.ID, verb), nil

	default:
		return "", fmt.Errorf("未知操作: %q。可选: list / add / delete / enable / disable", in.Action)
	}
}

func truncateCron(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
