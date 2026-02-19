package scheduler

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/robfig/cron/v3"

	"goclaw/internal/store"
)

// CronHandler 是 cron 任务触发时的回调函数类型。
// agentID: 执行任务的代理
// job: 触发的任务信息
type CronHandler func(ctx context.Context, agentID string, job *store.CronJob)

// CronScheduler 使用 robfig/cron 驱动所有 cron 定时任务。
// 支持标准 5 字段 cron 表达式（分 时 日 月 周）以及 @every 语法。
type CronScheduler struct {
	cr      *cron.Cron
	store   store.Store
	handler CronHandler

	mu      sync.Mutex
	entries map[string]cron.EntryID // job.ID -> cron entry ID（用于动态增删）
}

// NewCronScheduler 创建并返回一个新的 CronScheduler。
// handler 会在每次任务触发时被调用，由调用者负责实际的 AI 消息处理和渠道发送。
func NewCronScheduler(st store.Store, handler CronHandler) *CronScheduler {
	return &CronScheduler{
		// 使用秒级精度可选，这里用标准 5 字段（分 时 日 月 周），更接近常见 cron 用法
		cr:      cron.New(cron.WithLogger(cron.DefaultLogger)),
		store:   st,
		handler: handler,
		entries: make(map[string]cron.EntryID),
	}
}

// Start 从数据库加载所有已启用的 cron 任务并启动调度器。
// 会持续运行直到 ctx 取消。
func (cs *CronScheduler) Start(ctx context.Context) error {
	// 加载已有任务
	jobs, err := cs.store.ListAllCronJobs(ctx)
	if err != nil {
		return fmt.Errorf("加载 cron 任务失败: %w", err)
	}

	for _, job := range jobs {
		if job.Enabled {
			if err := cs.addJob(ctx, job); err != nil {
				log.Printf("[cron] 加载任务 %q 失败: %v", job.ID, err)
			}
		}
	}

	cs.cr.Start()
	log.Printf("[cron] 调度器已启动，已加载 %d 个任务", len(jobs))

	<-ctx.Done()
	log.Println("[cron] 正在停止调度器...")
	cs.cr.Stop()
	return nil
}

// AddJob 动态添加或更新一个 cron 任务（线程安全）。
func (cs *CronScheduler) AddJob(ctx context.Context, job *store.CronJob) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	// 如果已存在，先移除
	if entryID, ok := cs.entries[job.ID]; ok {
		cs.cr.Remove(entryID)
		delete(cs.entries, job.ID)
	}

	if !job.Enabled {
		return nil
	}

	return cs.addJob(ctx, job)
}

// RemoveJob 动态移除一个 cron 任务（线程安全）。
func (cs *CronScheduler) RemoveJob(jobID string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if entryID, ok := cs.entries[jobID]; ok {
		cs.cr.Remove(entryID)
		delete(cs.entries, jobID)
		log.Printf("[cron] 已移除任务: %s", jobID)
	}
}

// addJob 内部方法，假设已持有锁。
func (cs *CronScheduler) addJob(ctx context.Context, job *store.CronJob) error {
	jobCopy := *job // 避免闭包捕获可变变量
	entryID, err := cs.cr.AddFunc(job.CronExpr, func() {
		log.Printf("[cron] 触发任务: %s (%s)", jobCopy.ID, jobCopy.Name)
		cs.handler(ctx, jobCopy.AgentID, &jobCopy)
	})
	if err != nil {
		return fmt.Errorf("无效的 cron 表达式 %q: %w", job.CronExpr, err)
	}

	// 注意：调用者已持有 cs.mu 锁，这里直接修改 entries
	cs.entries[job.ID] = entryID

	log.Printf("[cron] 已注册任务: %s (%s) 表达式=%q", job.ID, job.Name, job.CronExpr)
	return nil
}

// ListEntries 返回当前所有已注册任务的调度状态（用于调试/工具显示）。
func (cs *CronScheduler) ListEntries() []cron.Entry {
	return cs.cr.Entries()
}
