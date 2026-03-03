package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"goclaw/internal/agent"
	"goclaw/internal/ai"
	"goclaw/internal/backup"
	"goclaw/internal/channels"
	"goclaw/internal/channels/dingtalk"
	"goclaw/internal/channels/feishu"
	"goclaw/internal/channels/telegram"
	"goclaw/internal/channels/wecom"
	"goclaw/internal/config"
	"goclaw/internal/scheduler"
	"goclaw/internal/store"
	"goclaw/internal/tools"
	"goclaw/internal/wslib"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

func main() {
	_ = godotenv.Load()

	root := newRootCmd()
	if err := root.Execute(); err != nil {
		log.Fatal(err)
	}
}

func newRootCmd() *cobra.Command {
	var cfgPath string

	root := &cobra.Command{
		Use:   "clawdbot",
		Short: "Go implementation of OpenClaw runtime",
	}
	root.PersistentFlags().StringVar(&cfgPath, "config", "", "config file path (default: ./clawdbot.json)")

	root.AddCommand(newStartCmd(&cfgPath))
	root.AddCommand(newSessionsCmd(&cfgPath))
	root.AddCommand(newRollbackCmd(&cfgPath))
	return root
}

func newStartCmd(cfgPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start goclaw runtime",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStart(*cfgPath)
		},
	}
}

func runStart(cfgPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config failed: %w", err)
	}
	if err := os.MkdirAll(cfg.Workspace, 0o755); err != nil {
		return fmt.Errorf("create workspace failed: %w", err)
	}

	projectRoot, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get cwd failed: %w", err)
	}
	projectRoot, err = filepath.Abs(projectRoot)
	if err != nil {
		return fmt.Errorf("resolve project root failed: %w", err)
	}

	st, err := store.NewSQLiteStore(cfg.DB)
	if err != nil {
		return fmt.Errorf("open db failed: %w", err)
	}
	defer st.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := st.Init(ctx); err != nil {
		return fmt.Errorf("init db failed: %w", err)
	}

	reg, cronTool, err := buildToolRegistry(cfg, st, projectRoot)
	if err != nil {
		return err
	}

	mgr, agentChannels, err := buildAgents(cfg, st, reg, projectRoot)
	if err != nil {
		return err
	}

	if err := mgr.CleanupOrphanedToolMessages(ctx, st); err != nil {
		log.Printf("[startup] cleanup orphaned tool messages failed: %v", err)
	}

	channelMap, err := buildChannels(cfg)
	if err != nil {
		return err
	}

	cronSched := scheduler.NewCronScheduler(st, cfg.Workspace, func(parent context.Context, agentID string, job *store.CronJob) {
		go executeCronJob(parent, mgr, channelMap, agentID, job)
	})
	cronTool.Sched = cronSched
	go func() {
		if err := cronSched.Start(ctx); err != nil && ctx.Err() == nil {
			log.Printf("[cron] scheduler stopped with error: %v", err)
		}
	}()

	var wg sync.WaitGroup
	for name, ch := range channelMap {
		if err := ch.Start(ctx); err != nil {
			return fmt.Errorf("start channel %s failed: %w", name, err)
		}
		log.Printf("[channel] started: %s", name)
		wg.Add(1)
		go func(ch channels.Channel) {
			defer wg.Done()
			routeChannelMessages(ctx, ch, mgr, agentChannels)
		}(ch)
	}

	log.Printf("[startup] goclaw started, agents=%d, channels=%d", len(agentChannels), len(channelMap))

	<-ctx.Done()
	log.Printf("[shutdown] stopping channels...")
	for _, ch := range channelMap {
		_ = ch.Stop()
	}
	wg.Wait()
	log.Printf("[shutdown] completed")
	return nil
}

func buildToolRegistry(cfg *config.Config, st store.Store, projectRoot string) (*tools.Registry, *tools.ManageCronTool, error) {
	reg := tools.NewRegistry()
	cronTool := &tools.ManageCronTool{Store: st}

	coreTools := []tools.Tool{
		&tools.BashTool{DefaultTimeout: 5 * time.Minute},
		&tools.WriteFileTool{
			ProjectRoot:      projectRoot,
			SkipConfirmation: cfg.SkipConfirmation,
		},
		&tools.AddProviderTool{},
		&tools.DeleteProviderTool{},
		&tools.SwitchModelTool{Store: st},
		&tools.ListAgentsTool{Store: st},
		&tools.CreateAgentTool{Store: st},
		&tools.ClearContextTool{Store: st},
		&tools.CompactContextTool{},
		cronTool,
	}
	for _, t := range coreTools {
		if err := reg.Register(t); err != nil {
			return nil, nil, fmt.Errorf("register core tool %s failed: %w", t.Name(), err)
		}
	}

	if err := tools.RegisterFeishuTools(reg, cfg); err != nil {
		return nil, nil, fmt.Errorf("register feishu tools failed: %w", err)
	}
	_, feishuEnabled := reg.Get("feishu_chat")
	if feishuEnabled {
		log.Printf("[startup] feishu tools registered")
	} else {
		log.Printf("[startup] feishu tools skipped (no app credentials)")
	}
	log.Printf("[startup] tool registry ready, total tools=%d", len(reg.Definitions()))

	return reg, cronTool, nil
}

func buildAgents(cfg *config.Config, st store.Store, reg *tools.Registry, projectRoot string) (*agent.Manager, map[string][]string, error) {
	defaultProvider, ok := cfg.DefaultProvider()
	if !ok {
		return nil, nil, fmt.Errorf("no provider configured: set PROVIDER_XXX_API_KEY in env")
	}

	mgr := agent.NewManager()
	agentChannels := make(map[string][]string)
	for _, ac := range cfg.Agents {
		agentID := strings.TrimSpace(ac.ID)
		if agentID == "" {
			continue
		}
		agentName := strings.TrimSpace(ac.Name)
		if agentName == "" {
			agentName = agentID
		}

		provider, model, providerID, err := buildProvider(defaultProvider, ac.Model)
		if err != nil {
			return nil, nil, fmt.Errorf("build provider for agent %s failed: %w", agentID, err)
		}

		workspaceRel := strings.TrimSpace(ac.Workspace)
		if workspaceRel == "" {
			workspaceRel = "."
		}
		workspaceRoot := filepath.Join(cfg.Workspace, workspaceRel)
		if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
			return nil, nil, fmt.Errorf("create agent workspace failed (%s): %w", agentID, err)
		}

		loader, err := wslib.NewLoader(workspaceRoot)
		if err != nil {
			return nil, nil, fmt.Errorf("create loader for %s failed: %w", agentID, err)
		}

		a := agent.New(agent.Config{
			ID:            agentID,
			Name:          agentName,
			Provider:      provider,
			ProviderID:    providerID,
			Store:         st,
			Registry:      reg,
			Loader:        loader,
			Concurrency:   cfg.Concurrency,
			ContextWindow: cfg.ContextWindow,
			ProjectRoot:   projectRoot,
			Model:         model,
			Cfg:           cfg,
		})
		mgr.Register(a)

		chs := normalizeChannels(ac.Channels)
		if len(chs) == 0 {
			chs = []string{"feishu", "telegram", "dingtalk", "wecom"}
		}
		agentChannels[agentID] = chs

		log.Printf("[startup] agent ready: id=%s name=%s model=%s provider=%s workspace=%s channels=%s",
			agentID, agentName, model, providerID, workspaceRoot, strings.Join(chs, ","))
	}

	if len(agentChannels) == 0 {
		return nil, nil, fmt.Errorf("no agent configured")
	}
	return mgr, agentChannels, nil
}

func buildProvider(defaultProvider *config.Provider, modelOverride string) (ai.Provider, string, string, error) {
	model := strings.TrimSpace(modelOverride)
	if model == "" {
		model = strings.TrimSpace(defaultProvider.Model)
	}
	if model == "" {
		return nil, "", "", fmt.Errorf("provider %s has empty model", defaultProvider.Name)
	}

	opts := &ai.ProviderOptions{ContextWindow: defaultProvider.MaxContext}
	switch strings.ToLower(strings.TrimSpace(defaultProvider.APIType)) {
	case "openai":
		return ai.NewOpenAIProvider(defaultProvider.APIKey, defaultProvider.BaseURL, model, opts), model, defaultProvider.Name, nil
	case "anthropic":
		return ai.NewAnthropicProvider(defaultProvider.APIKey, defaultProvider.BaseURL, model, opts), model, defaultProvider.Name, nil
	default:
		return nil, "", "", fmt.Errorf("unsupported provider type: %s", defaultProvider.APIType)
	}
}

func buildChannels(cfg *config.Config) (map[string]channels.Channel, error) {
	channelMap := make(map[string]channels.Channel)

	if cfg.Channels.Feishu.Enabled || (strings.TrimSpace(cfg.Channels.Feishu.AppID) != "" && strings.TrimSpace(cfg.Channels.Feishu.AppSecret) != "") {
		adapter, err := feishu.New(cfg)
		if err != nil {
			return nil, fmt.Errorf("init feishu channel failed: %w", err)
		}
		channelMap[adapter.Name()] = adapter
	}
	if cfg.Channels.Telegram.Enabled || strings.TrimSpace(cfg.Channels.Telegram.Token) != "" {
		adapter, err := telegram.New(cfg.Channels.Telegram)
		if err != nil {
			return nil, fmt.Errorf("init telegram channel failed: %w", err)
		}
		channelMap[adapter.Name()] = adapter
	}
	if cfg.Channels.DingTalk.Enabled {
		adapter, err := dingtalk.New(cfg)
		if err != nil {
			return nil, fmt.Errorf("init dingtalk channel failed: %w", err)
		}
		channelMap[adapter.Name()] = adapter
	}
	if cfg.Channels.WeCom.Enabled {
		adapter, err := wecom.New(cfg)
		if err != nil {
			return nil, fmt.Errorf("init wecom channel failed: %w", err)
		}
		channelMap[adapter.Name()] = adapter
	}

	if len(channelMap) == 0 {
		return nil, fmt.Errorf("no enabled channel found")
	}
	return channelMap, nil
}

func routeChannelMessages(ctx context.Context, ch channels.Channel, mgr *agent.Manager, agentChannels map[string][]string) {
	msgs := ch.Messages()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-msgs:
			if !ok {
				return
			}
			m := msg
			targetAgents := mgr.GetForChannel(ch.Name(), agentChannels)
			if len(targetAgents) == 0 {
				log.Printf("[route] no agent mapped for channel=%s", ch.Name())
				continue
			}

			for _, a := range targetAgents {
				a := a
				go func() {
					sessionID := agent.SessionID(m.Channel, m.ChatID, a.ID())
					resultCh := a.HandleMessageAsync(ctx, sessionID, m.Content, agent.IsMainSession(m.ChatType))

					select {
					case <-ctx.Done():
						return
					case res := <-resultCh:
						if res.Err != nil {
							log.Printf("[agent:%s] handle message failed: %v", a.ID(), res.Err)
							return
						}
						reply := strings.TrimSpace(res.Text)
						if reply == "" {
							return
						}
						if len(targetAgents) > 1 {
							reply = fmt.Sprintf("[%s]\n%s", a.Name(), reply)
						}
						if err := ch.Send(ctx, m.ChatID, reply); err != nil {
							log.Printf("[channel:%s] send failed: %v", ch.Name(), err)
						}
					}
				}()
			}
		}
	}
}

func executeCronJob(parent context.Context, mgr *agent.Manager, channelMap map[string]channels.Channel, agentID string, job *store.CronJob) {
	a, ok := mgr.Get(agentID)
	if !ok {
		log.Printf("[cron] agent not found: %s", agentID)
		return
	}

	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()

	sessionID := agent.SessionID("cron", job.ID, agentID)
	resp, err := a.HandleMessage(ctx, sessionID, job.Prompt, true)
	if err != nil {
		log.Printf("[cron] agent execution failed (job=%s agent=%s): %v", job.ID, agentID, err)
		resp = fmt.Sprintf("cron job %s failed: %v", job.ID, err)
	}

	if strings.TrimSpace(job.ChannelID) == "" {
		return
	}
	tg, ok := channelMap["telegram"]
	if !ok {
		log.Printf("[cron] telegram channel not enabled, skip send (job=%s)", job.ID)
		return
	}
	if err := tg.Send(ctx, job.ChannelID, resp); err != nil {
		log.Printf("[cron] send result failed (job=%s): %v", job.ID, err)
	}
}

func normalizeChannels(channelsIn []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(channelsIn))
	for _, c := range channelsIn {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "" {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out
}

func newSessionsCmd(cfgPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sessions",
		Short: "Session management commands",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List stored sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runListSessions(*cfgPath)
		},
	})
	return cmd
}

func runListSessions(cfgPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	st, err := store.NewSQLiteStore(cfg.DB)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()
	if err := st.Init(ctx); err != nil {
		return err
	}

	sessions, err := st.ListSessions(ctx)
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		fmt.Println("no sessions")
		return nil
	}
	for _, s := range sessions {
		lock := "unlocked"
		if s.Locked {
			lock = "locked"
		}
		fmt.Printf("%s\t%s\tupdated=%s\n", s.ID, lock, s.UpdatedAt.Format(time.RFC3339))
	}
	return nil
}

func newRollbackCmd(cfgPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rollback [tag]",
		Short: "Rollback workspace to last-good or specified checkpoint",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tag := ""
			if len(args) == 1 {
				tag = args[0]
			}
			return runRollback(*cfgPath, tag)
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List available checkpoint tags",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRollbackList(*cfgPath)
		},
	})
	return cmd
}

func runRollback(cfgPath, tag string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	exe, _ := os.Executable()

	bm := backup.New(cfg.Workspace, exe, cfg.Backup.MaxVersions, true)
	if err := bm.InitWorkspaceGit(); err != nil {
		return err
	}
	if err := bm.Rollback(tag); err != nil {
		return err
	}
	if tag == "" {
		tag = "last-good"
	}
	fmt.Printf("rollback completed: %s\n", tag)
	return nil
}

func runRollbackList(cfgPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	exe, _ := os.Executable()

	bm := backup.New(cfg.Workspace, exe, cfg.Backup.MaxVersions, true)
	if err := bm.InitWorkspaceGit(); err != nil {
		return err
	}
	tags, err := bm.ListCheckpoints()
	if err != nil {
		return err
	}
	if len(tags) == 0 {
		fmt.Println("no checkpoints")
		return nil
	}
	for _, t := range tags {
		fmt.Println(t)
	}
	return nil
}
