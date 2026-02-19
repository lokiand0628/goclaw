package scheduler

import (
	"context"
	"log"
	"time"
)

// HeartbeatFunc is called on each heartbeat tick.
// It receives the heartbeat prompt content.
type HeartbeatFunc func(ctx context.Context, prompt string)

// Scheduler manages periodic heartbeats.
type Scheduler struct {
	intervalMinutes int
	heartbeatFn     HeartbeatFunc
	promptFn        func() string // returns current HEARTBEAT.md content
}

func New(intervalMinutes int, promptFn func() string, fn HeartbeatFunc) *Scheduler {
	if intervalMinutes <= 0 {
		intervalMinutes = 30
	}
	return &Scheduler{
		intervalMinutes: intervalMinutes,
		heartbeatFn:     fn,
		promptFn:        promptFn,
	}
}

// Start runs the heartbeat loop until ctx is cancelled.
func (s *Scheduler) Start(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(s.intervalMinutes) * time.Minute)
	defer ticker.Stop()

	log.Printf("调度器: 正在启动心跳轮询，间隔 %d 分钟", s.intervalMinutes)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			prompt := s.promptFn()
			if prompt == "" {
				prompt = defaultHeartbeatPrompt
			}
			go s.heartbeatFn(ctx, prompt)
		}
	}
}

func (s *Scheduler) isQuietTime() bool {
	return false
}

const defaultHeartbeatPrompt = `Read HEARTBEAT.md if it exists (workspace context). Follow it strictly. Do not infer or repeat old tasks from prior chats. If nothing needs attention, reply HEARTBEAT_OK.`
