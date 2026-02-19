package wslib

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Loader reads workspace markdown files and assembles a system prompt.
type Loader struct {
	root string // absolute path to the agent's workspace directory
}

// NewLoader creates a loader for the given workspace root directory.
func NewLoader(root string) (*Loader, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace path: %w", err)
	}
	return &Loader{root: abs}, nil
}

// Root returns the absolute workspace root path.
func (l *Loader) Root() string { return l.root }

// RuntimeConfig 包含智能体的实时运行状态。
type RuntimeConfig struct {
	AgentID      string
	Model        string
	ContextLimit int
	CurrentTime  time.Time
}

// SystemPrompt assembles the system prompt from workspace files.
// isMainSession controls whether MEMORY.md is loaded (see AGENTS.md security note).
// extraContext 若非空，则作为"对话历史摘要"区块追加到提示词末尾（用于上下文压缩场景）。
func (l *Loader) SystemPrompt(rc RuntimeConfig, isMainSession bool, extraContext string) (string, error) {
	var parts []string

	// 1. 系统指令 (System Commands) - 物理隔离与权限认知
	// AI 可通过工具管理代理，但由于代码级的“安全锁”，AI 已不再具备直接修改模型调用的工具。
	commands := "## System Commands\n" +
		"- 我可执行: 通过内置工具管理代理、处理工作区文件。\n" +
		"- 我不可执行（必须引导用户输入）: `/model` (切换模型/供应商), `/clear` (重置会话)。\n" +
		"**提示**: 如遇无法确定的环境状态，请引导用户输入 `/status` 查看。"
	parts = append(parts, commands)

	// Load in the order specified by AGENTS.md
	for _, name := range []string{"IDENTITY.md", "SOUL.md", "USER.md", "AGENTS.md", "TOOLS.md"} {
		content, err := l.readFile(name)
		if err != nil {
			continue // file may not exist yet
		}
		parts = append(parts, fmt.Sprintf("## %s\n\n%s", name, content))
	}

	// Recent memory: today and yesterday
	now := time.Now()
	for _, t := range []time.Time{now, now.AddDate(0, 0, -1)} {
		name := fmt.Sprintf("memory/%s.md", t.Format("2006-01-02"))
		content, err := l.readFile(name)
		if err != nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("## %s\n\n%s", name, content))
	}

	// MEMORY.md only in main session
	if isMainSession {
		content, err := l.readFile("MEMORY.md")
		if err == nil {
			parts = append(parts, fmt.Sprintf("## MEMORY.md (Long-term Memory)\n\n%s", content))
		}
	}

	// HEARTBEAT.md if present
	hb, err := l.readFile("HEARTBEAT.md")
	if err == nil && strings.TrimSpace(hb) != "" {
		parts = append(parts, fmt.Sprintf("## HEARTBEAT.md\n\n%s", hb))
	}

	// 若存在压缩后的历史摘要，追加到提示词末尾（不污染历史消息体，不破坏角色交替）
	if extraContext != "" {
		parts = append(parts, fmt.Sprintf("## 对话历史摘要（压缩）\n\n%s", extraContext))
	}

	full := strings.Join(parts, "\n\n---\n\n")
	// 粗略检查：如果系统提示词过长（超过 100KB），记录警告。
	// 大多数现代模型支持 128K+ 上下文，但系统提示词占位过多会压缩对话空间。
	if len(full) > 100_000 {
		fmt.Printf("⚠️ 警告: 系统提示词过长 (%d 字节)，可能导致 API 响应缓慢或 400 错误。\n", len(full))
	}
	return full, nil
}

// ReadFile reads a workspace-relative file.
func (l *Loader) ReadFile(relPath string) (string, error) {
	return l.readFile(relPath)
}

// WriteFile writes content to a workspace-relative file (used by agent for self-modification).
func (l *Loader) WriteFile(relPath, content string) error {
	abs := filepath.Join(l.root, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	return os.WriteFile(abs, []byte(content), 0644)
}

// AppendMemory appends a log entry to today's memory file.
func (l *Loader) AppendMemory(entry string) error {
	name := fmt.Sprintf("memory/%s.md", time.Now().Format("2006-01-02"))
	abs := filepath.Join(l.root, name)
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		return err
	}

	f, err := os.OpenFile(abs, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = fmt.Fprintf(f, "\n%s\n", entry)
	return err
}

// HeartbeatContent returns the contents of HEARTBEAT.md, or empty string.
func (l *Loader) HeartbeatContent() string {
	content, err := l.readFile("HEARTBEAT.md")
	if err != nil {
		return ""
	}
	return content
}

// SkillsDir returns the path to the skills directory.
func (l *Loader) SkillsDir() string {
	return filepath.Join(l.root, "skills")
}

// ScriptsDir returns the path to the scripts directory.
func (l *Loader) ScriptsDir() string {
	return filepath.Join(l.root, "scripts")
}

func (l *Loader) readFile(relPath string) (string, error) {
	abs := filepath.Join(l.root, relPath)
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
