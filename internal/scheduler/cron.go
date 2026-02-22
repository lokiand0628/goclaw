package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/robfig/cron/v3"

	"goclaw/internal/logx"
	"goclaw/internal/store"
)

// CronHandler 是 cron 任务触发时的回调函数类型。
// agentID: 执行任务的代理
// job: 触发的任务信息
type CronHandler func(ctx context.Context, agentID string, job *store.CronJob)

// CronScheduler 使用 robfig/cron 驱动所有 cron 定时任务。
type CronScheduler struct {
	cr      *cron.Cron
	store   store.Store
	handler CronHandler

	mu       sync.Mutex
	entries  map[string]cron.EntryID // job.ID -> cron entry ID
	cronPath string                  // JSON 文件存储路径
}

func NewCronScheduler(st store.Store, workspace string, handler CronHandler) *CronScheduler {
	return &CronScheduler{
		cr:       cron.New(cron.WithLogger(cron.DefaultLogger)),
		store:    st,
		handler:  handler,
		entries:  make(map[string]cron.EntryID),
		cronPath: filepath.Join(workspace, "cron_jobs.json"),
	}
}

func (cs *CronScheduler) Start(ctx context.Context) error {
	jobs, err := cs.store.ListAllCronJobs(ctx)
	if err != nil {
		return fmt.Errorf("[cron] 从数据库加载任务失败: %w", err)
	}

	// 兼容旧版本：如果 DB 中没有任务，则尝试从 legacy JSON 导入一次。
	if len(jobs) == 0 {
		legacyJobs, legacyErr := cs.loadJobs()
		if legacyErr == nil && len(legacyJobs) > 0 {
			for _, j := range legacyJobs {
				if err := cs.store.SaveCronJob(ctx, j); err != nil {
					log.Printf("[cron] 导入 legacy 任务 %s 失败: %v", j.ID, err)
					continue
				}
			}
			jobs, _ = cs.store.ListAllCronJobs(ctx)
			log.Printf("[cron] 已从 legacy JSON 导入 %d 个任务到数据库", len(jobs))
		}
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
		logx.Info("cron_job_triggered", "job_id", jobCopy.ID, "agent_id", jobCopy.AgentID)
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

func (cs *CronScheduler) loadJobs() ([]*store.CronJob, error) {
	data, err := os.ReadFile(cs.cronPath)
	if err != nil {
		return nil, err
	}
	var jobs []*store.CronJob
	if err := json.Unmarshal(data, &jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (cs *CronScheduler) SaveJob(job *store.CronJob) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.store.SaveCronJob(context.Background(), job)
}

func (cs *CronScheduler) DeleteJob(id string) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.store.DeleteCronJob(context.Background(), id)
}

func (cs *CronScheduler) ListJobs() ([]*store.CronJob, error) {
	return cs.store.ListAllCronJobs(context.Background())
}

// ListEntries 返回当前所有已注册任务的调度状态（用于调试/工具显示）。
func (cs *CronScheduler) ListEntries() []cron.Entry {
	return cs.cr.Entries()
}
