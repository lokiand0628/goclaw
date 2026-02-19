package agent

import (
	"context"
	"time"

	"goclaw/internal/store"
	"goclaw/internal/wslib"

	"golang.org/x/sync/semaphore"
)

// storeSession 和 storeMsg 是为了测试兼容性的类型别名。
type storeSession = store.Session
type storeMsg = store.Message

// storeAdapter 为代理使用封装了 store.Store 接口。
type storeAdapter interface {
	GetOrCreateSession(ctx context.Context, id string) (*store.Session, error)
	LockSession(ctx context.Context, id string) error
	UnlockSession(ctx context.Context, id string) error
	UnlockStaleSessions(ctx context.Context) error
	DeleteSession(ctx context.Context, id string) error
	ListSessions(ctx context.Context) ([]*store.Session, error)
	AddMessage(ctx context.Context, sessionID, role, content string) error
	AddComplexMessage(ctx context.Context, sessionID, role, content, msgType string) error
	GetMessages(ctx context.Context, sessionID string) ([]*store.Message, error)
	PruneLastMessage(ctx context.Context, sessionID string) error
	TruncateMessages(ctx context.Context, sessionID string, keep int) error
	Init(ctx context.Context) error
	Close() error
}

// workspaceAdapter 是用于测试的最小化 workspace.Loader 适配器。
type workspaceAdapter struct {
	soul string
}

func (w *workspaceAdapter) SystemPrompt(_ wslib.RuntimeConfig, _ bool, _ string) (string, error) {
	return w.soul, nil
}
func (w *workspaceAdapter) Root() string { return "/tmp/workspace" }

// loaderIface 为代理抽象了 workspace.Loader。
type loaderIface interface {
	SystemPrompt(rc wslib.RuntimeConfig, isMainSession bool, extraContext string) (string, error)
	Root() string
}

// newSem 创建一个具有给定权重的信号量。
func newSem(n int64) *semaphore.Weighted {
	return semaphore.NewWeighted(n)
}

// adaptStore 将 mockStore 类类型转换为 storeAdapter。
// 在生产环境中，store.SQLiteStore 直接实现此接口。
func adaptStore(s storeAdapter) storeAdapter { return s }

// mockStore 在 loop_test.go 中定义，并为测试实现 storeAdapter。

// SessionID 为渠道消息构建规范的会话 ID。
func SessionID(channelName, chatID, agentID string) string {
	return agentID + "/" + channelName + "/" + chatID
}

// IsMainSession 如果此会话是直接 (1:1) 对话，则返回 true。
// 群聊不属于主会话。
func IsMainSession(chatType string) bool {
	return chatType != "group" && chatType != "channel"
}

// ContextKey 用于上下文值传递。
type ContextKey string

const (
	ContextKeyAgentID ContextKey = "agent_id"
	ContextKeyChannel ContextKey = "channel"
)

// HeartbeatPrompt 是默认的心跳触发消息。
const HeartbeatPrompt = `如果 HEARTBEAT.md 存在（工作区上下文），请阅读它。严格遵守其中的内容。不要从之前的聊天中推断或重复旧任务。如果不需要注意任何事情，回复 HEARTBEAT_OK。`

// UpgradePrompt 在 AI 即将修改其自身代码时添加在前面。
const UpgradePrompt = `你即将修改机器人的源代码或工作区。
规则：
1. 在做出更改之前，始终进行一次 git tag pre-change-<timestamp>
2. 更改后，运行 go build 以验证编译
3. 在重启之前向用户报告结果
4. 如果构建失败，解释错误并从备份中恢复
`

// startupTime 用于健康检查。
var startupTime = time.Now()

// Uptime 返回代理已运行的时间。
func Uptime() time.Duration {
	return time.Since(startupTime)
}

// truncate 为了日志记录而将字符串缩短到最大 n 个字符。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
