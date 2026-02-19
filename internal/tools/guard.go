package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"goclaw/internal/ai"
)

// Tool 是代理可以调用的可执行工具接口。
type Tool interface {
	Name() string
	Description() string
	InputSchema() json.RawMessage
	Execute(input json.RawMessage, ctx *ExecContext) (string, error)
}

// ExecContext 为工具执行提供运行时上下文。
type ExecContext struct {
	WorkspaceRoot string // 代理工作区的绝对路径
	ProjectRoot   string // clawdbot 源代码的绝对路径 (用于自我修改检测)
	SessionID     string
	AgentID       string // 调用工具的代理 ID
	// CompactFunc 允许工具触发当前会话的上下文压缩（由 agent 层注入）
	CompactFunc func(ctx context.Context, sessionID string) error
}

// Registry 保存所有注册的工具。
type Registry struct {
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

func (r *Registry) Register(t Tool) error {
	if _, exists := r.tools[t.Name()]; exists {
		return fmt.Errorf("工具 %q 已注册", t.Name())
	}
	r.tools[t.Name()] = t
	return nil
}

func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Definitions 返回所有已注册工具的 ai.ToolDef 切片。
func (r *Registry) Definitions() []ai.ToolDef {
	defs := make([]ai.ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, ai.ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}
	return defs
}

// ---- 守护规则：AI 不能触碰的内容 ----

// hardBlockedPaths 列出了 AI 永远不能直接修改的路径模式。
// 这些路径是相对于项目根目录的。
// 设计原则：只保护"系统契约"文件（由人类定义的规则/约束）。
// AI 应自由维护自我定义文件（SOUL.md、USER.md、MEMORY.md等）。
var hardBlockedPaths = []string{
	".env",
	"go.mod",
	"go.sum",
	"cmd/clawdbot/main.go",
	"internal/tools/guard.go", // 自我引用：防止禁用安全保护
	"Dockerfile",
	"docker-compose.yml",
	"docker-compose.yaml",
	// AGENTS.md 是系统行为契约（操作规范），由人类维护。
	// AI 可以提议修改，但不能直接写入。
	"workspace/AGENTS.md",
}

// CheckPathAllowed 如果 AI 不应该写入给定路径，则返回错误。
// absPath 应该是绝对路径。
func CheckPathAllowed(absPath, projectRoot string) error {
	// 规范化
	absPath = filepath.Clean(absPath)
	projectRoot = filepath.Clean(projectRoot)

	rel, err := filepath.Rel(projectRoot, absPath)
	if err != nil {
		// 如果不是相对于项目根目录的，则允许 (属于工作区)
		return nil
	}

	// 检查硬性阻止的路径
	for _, blocked := range hardBlockedPaths {
		if rel == blocked || rel == filepath.FromSlash(blocked) {
			return fmt.Errorf("🔴 已拦截: %q 是受保护的系统文件，AI 无法修改", rel)
		}
	}

	// 拦截任何地方的 .env 文件
	if filepath.Base(absPath) == ".env" || strings.HasSuffix(absPath, ".env") {
		return fmt.Errorf("🔴 已拦截: .env 文件包含凭据，AI 无法修改")
	}

	return nil
}

// NeedsConfirmation 如果操作需要用户确认，则返回 true。
// 根据 projectRoot 检查 confirmPaths。
// 如果 skipAll 为 true，则始终返回 false（除非是硬性阻止的路径，但这应该在 CheckPathAllowed 中捕获）。
func NeedsConfirmation(absPath, projectRoot string, skipAll bool) bool {
	if skipAll {
		return false
	}

	rel, err := filepath.Rel(projectRoot, absPath)
	if err != nil {
		return false
	}

	// 仅对 .go 源文件的更改需要确认
	if strings.HasSuffix(rel, ".go") {
		return true
	}

	// 以前我们保护 SOUL.md 和 USER.md，现在根据用户反馈已放开。

	return false
}
