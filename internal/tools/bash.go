package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// BashTool 执行 shell 命令。
// 它遵守文件修改的守护规则。
type BashTool struct {
	DefaultTimeout time.Duration
	MaxOutputBytes int
}

func (b *BashTool) Name() string { return "bash" }

func (b *BashTool) Description() string {
	return `执行 shell 命令。工作目录是代理的工作区根目录。
用于：文件操作、运行脚本、安装包、修改工作区文件。
注意：禁止写入受保护的系统文件（例如 .env、go.mod、Dockerfile）。
修改 .go 源文件需要先获得用户确认。`
}

func (b *BashTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {
				"type": "string",
				"description": "要执行的 shell 命令"
			},
			"timeout_seconds": {
				"type": "integer",
				"description": "可选的超时时间（秒，默认：300）"
			}
		},
		"required": ["command"]
	}`)
}

type bashInput struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type limitWriter struct {
	buf       bytes.Buffer
	remaining int
	truncated bool
}

func (w *limitWriter) Write(p []byte) (int, error) {
	if w.remaining <= 0 {
		w.truncated = true
		return len(p), nil
	}
	if len(p) > w.remaining {
		w.buf.Write(p[:w.remaining])
		w.remaining = 0
		w.truncated = true
		return len(p), nil
	}
	n, _ := w.buf.Write(p)
	w.remaining -= n
	return len(p), nil
}

func (w *limitWriter) String() string {
	if w.truncated {
		return w.buf.String() + "\n... [输出已截断]"
	}
	return w.buf.String()
}

func (b *BashTool) resolveMaxOutput() int {
	if b.MaxOutputBytes > 0 {
		return b.MaxOutputBytes
	}
	if v := strings.TrimSpace(os.Getenv("CLAWDBOT_BASH_MAX_OUTPUT_BYTES")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 200_000
}

func (b *BashTool) Execute(input json.RawMessage, ctx *ExecContext) (string, error) {
	var in bashInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("无效的 bash 输入: %w", err)
	}

	// 守护：检测对受保护路径的写入
	if err := b.checkCommandSafety(in.Command); err != nil {
		return "", err
	}

	timeout := b.DefaultTimeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	if in.TimeoutSeconds > 0 {
		timeout = time.Duration(in.TimeoutSeconds) * time.Second
	}

	execCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "bash", "-c", in.Command)
	cmd.Dir = ctx.WorkspaceRoot
	cmd.Env = append(os.Environ(),
		"WORKSPACE="+ctx.WorkspaceRoot,
	)

	maxOutput := b.resolveMaxOutput()
	stdout := &limitWriter{remaining: maxOutput}
	stderr := &limitWriter{remaining: maxOutput}

	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()

	var result strings.Builder
	outStr := stdout.String()
	if len(outStr) > 0 {
		result.WriteString(outStr)
	}

	errStr := stderr.String()
	if len(errStr) > 0 {
		if result.Len() > 0 {
			result.WriteString("\n")
		}
		result.WriteString("[stderr]\n")
		result.WriteString(errStr)
	}

	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			return result.String(), fmt.Errorf("命令在 %s 后超时", timeout)
		}
		// 即使退出状态码非零也返回输出，以便 AI 查看错误原因
		return result.String(), fmt.Errorf("退出状态: %w", err)
	}

	return result.String(), nil
}

// checkCommandSafety 分析命令是否尝试写入受保护的路径。
// 这是一种尽力而为的启发式检查；真正的守护是 Docker 中的 OS 权限。
func (b *BashTool) checkCommandSafety(command string) error {
	// 涉及受保护文件的危险模式列表
	writePatterns := []string{
		"> .env", ">> .env",
		" .env ", "/.env",
		"go.mod", "go.sum",
		"docker-compose",
		"Dockerfile",
	}

	for _, p := range writePatterns {
		if strings.Contains(command, p) {
			// 仅当看起来像写入操作时才进行拦截
			writeOps := []string{">", "tee ", "sed -i", "echo ", "cat >", "printf "}
			for _, op := range writeOps {
				if strings.Contains(command, op) {
					return fmt.Errorf("🔴 已拦截: 命令似乎在修改受保护文件 (%q)。请使用用户批准的方法处理凭据", p)
				}
			}
		}
	}

	return nil
}

// WriteFileTool 允许代理在强制执行守护规则的情况下写入文件。
type WriteFileTool struct {
	ProjectRoot      string
	SkipConfirmation bool
}

func (w *WriteFileTool) Name() string { return "write_file" }

func (w *WriteFileTool) Description() string {
	return `在工作区中向文件写入内容。
受保护的系统文件（如 .env、go.mod、Dockerfile、main.go）无法写入。
修改 .go 源文件需要先获得用户确认。`
}

func (w *WriteFileTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "文件路径，相对于工作区根目录"
			},
			"content": {
				"type": "string",
				"description": "要写入的内容"
			},
			"append": {
				"type": "boolean",
				"description": "如果为 true，则追加到文件末尾而不是覆盖"
			}
		},
		"required": ["path", "content"]
	}`)
}

type writeFileInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Append  bool   `json:"append"`
}

func (w *WriteFileTool) Execute(input json.RawMessage, ctx *ExecContext) (string, error) {
	var in writeFileInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("无效的 write_file 输入: %w", err)
	}

	// 解析路径
	absPath := in.Path
	if !strings.HasPrefix(absPath, "/") {
		absPath = ctx.WorkspaceRoot + "/" + in.Path
	}

	// 守护检查
	if err := CheckPathAllowed(absPath, ctx.ProjectRoot); err != nil {
		return "", err
	}
	if NeedsConfirmation(absPath, ctx.ProjectRoot, w.SkipConfirmation) {
		return "", fmt.Errorf("🟡 需要确认: 修改 %q 需要用户确认。请在继续之前询问用户", in.Path)
	}

	// 写入
	if err := os.MkdirAll(w.projectDir(absPath), 0755); err != nil {
		return "", fmt.Errorf("创建目录失败: %w", err)
	}

	flag := os.O_WRONLY | os.O_CREATE
	if in.Append {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}

	f, err := os.OpenFile(absPath, flag, 0644)
	if err != nil {
		return "", fmt.Errorf("打开文件失败: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(in.Content); err != nil {
		return "", fmt.Errorf("写入失败: %w", err)
	}

	action := "写入"
	if in.Append {
		action = "追加"
	}
	return fmt.Sprintf("✅ 已%s到 %s (%d 字节)", action, in.Path, len(in.Content)), nil
}

func (w *WriteFileTool) projectDir(path string) string {
	dir := path[:strings.LastIndex(path, "/")]
	if dir == "" {
		return "."
	}
	return dir
}
