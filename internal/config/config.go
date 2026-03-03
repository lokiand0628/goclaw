package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Config 是 clawdbot 的顶级配置。
// 环境变量的优先级高于 JSON 配置文件。
type Config struct {
	// 核心路径
	Workspace string `json:"workspace"` // 工作区目录路径 (CLAWDBOT_WORKSPACE)
	DB        string `json:"db"`        // SQLite 数据库路径 (CLAWDBOT_DB)

	// 并发
	Concurrency   int `json:"concurrency"`   // 最大并行 AI 调用数 (CLAWDBOT_CONCURRENCY, 默认 4)
	ContextWindow int `json:"contextWindow"` // 历史记录中的最大消息数 (默认 50)

	// LLM 提供商
	Models ModelsConfig `json:"models"`

	// 渠道
	Channels ChannelsConfig `json:"channels"`

	// 代理
	Agents []AgentConfig `json:"agents"`

	// 心跳 / 定时调度器
	Heartbeat HeartbeatConfig `json:"heartbeat"`

	// 备份 / 回滚
	Backup BackupConfig `json:"backup"`

	// 安全
	SkipConfirmation bool `json:"skipConfirmation"` // CLAWDBOT_SKIP_CONFIRMATION

	// provider 缓存（从 ENV 解析）
	providers map[string]*Provider
}

type ModelsConfig struct {
	Providers map[string]ProviderConfig `json:"providers"`
}

// Provider 定义从 ENV 解析的 provider 配置
type Provider struct {
	Name       string
	BaseURL    string
	APIKey     string
	Model      string
	APIType    string // "anthropic" 或 "openai"
	MaxContext int    // 上下文窗口，0 表示使用内置预设
}

// ProviderConfig 是旧版配置结构，保留用于兼容性
type ProviderConfig struct {
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey"`
	Model   string `json:"model"`
	API     string `json:"api"`
}

type ChannelsConfig struct {
	Feishu   FeishuConfig   `json:"feishu"`
	Telegram TelegramConfig `json:"telegram"`
	DingTalk DingTalkConfig `json:"dingtalk"`
	WeCom    WeComConfig    `json:"wecom"`
}

type FeishuConfig struct {
	Enabled   bool   `json:"enabled"`
	AppID     string `json:"appId"`
	AppSecret string `json:"appSecret"`
	// lark (国际版) vs feishu (国内版). 默认: feishu
	Domain string `json:"domain"`
	// 可选：覆盖 OpenAPI Base URL（主要用于测试/代理）。
	OpenBaseURL string `json:"openBaseURL"`
}

type TelegramConfig struct {
	Enabled      bool     `json:"enabled"`
	Token        string   `json:"token"`
	AllowedUsers []string `json:"allowedUsers"`
}

type DingTalkConfig struct {
	Enabled   bool   `json:"enabled"`
	AppKey    string `json:"appKey"`
	AppSecret string `json:"appSecret"`
	// 用于发送消息的机器人 webhook 令牌
	RobotToken string `json:"robotToken"`
}

type WeComConfig struct {
	Enabled bool   `json:"enabled"`
	CorpID  string `json:"corpId"`
	AgentID int    `json:"agentId"`
	Secret  string `json:"secret"`
}

type AgentConfig struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Workspace string   `json:"workspace"` // 相对于全局工作区根目录的路径
	Channels  []string `json:"channels"`  // 此代理监听的渠道名称
	Model     string   `json:"model"`     // 覆盖全局模型
}

type HeartbeatConfig struct {
	IntervalMinutes int    `json:"intervalMinutes"` // CLAWDBOT_HEARTBEAT_INTERVAL, 默认 30
	QuietStart      int    `json:"-"`               // 已废弃
	QuietEnd        int    `json:"-"`               // 已废弃
	AdminChatID     string `json:"adminChatId"`     // 可选：在此发送心跳通知
}

type BackupConfig struct {
	Enabled     bool `json:"enabled"`     // CLAWDBOT_BACKUP_ENABLED, 默认 true
	MaxVersions int  `json:"maxVersions"` // CLAWDBOT_BACKUP_MAX_VERSIONS, 默认 10
}

// DefaultConfigPath 返回 ./clawdbot.json
func DefaultConfigPath() string {
	return "clawdbot.json"
}

// Load 从 path 处的 JSON 文件读取配置，然后覆盖环境变量。
func Load(path string) (*Config, error) {
	cfg := defaults()

	// 如果 JSON 文件存在，则加载它
	if path == "" {
		path = DefaultConfigPath()
	}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("解析配置 %s 失败: %w", path, err)
		}
	}

	// 覆盖环境变量 (最高优先级)
	overlayEnv(cfg)

	return cfg, nil
}

func defaults() *Config {
	return &Config{
		Workspace:     "./workspace",
		DB:            "./clawdbot.db",
		Concurrency:   4,
		ContextWindow: 50,
		Heartbeat: HeartbeatConfig{
			IntervalMinutes: 30,
			QuietStart:      23,
			QuietEnd:        8,
		},
		Backup: BackupConfig{
			Enabled:     true,
			MaxVersions: 10,
		},
		Agents: []AgentConfig{
			{
				ID:        "main",
				Name:      "main",
				Workspace: ".",
				Channels:  []string{"feishu", "telegram", "dingtalk", "wecom"},
			},
		},
	}
}

func overlayEnv(cfg *Config) {
	if v := os.Getenv("CLAWDBOT_WORKSPACE"); v != "" {
		cfg.Workspace = expandHome(v)
	}
	if v := os.Getenv("CLAWDBOT_DB"); v != "" {
		cfg.DB = expandHome(v)
	}
	if v := os.Getenv("CLAWDBOT_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Concurrency = n
		}
	}

	// 从环境变量加载渠道凭据 (覆盖 JSON)
	if v := os.Getenv("FEISHU_APP_ID"); v != "" {
		cfg.Channels.Feishu.AppID = v
		cfg.Channels.Feishu.Enabled = true
	}
	if v := os.Getenv("FEISHU_APP_SECRET"); v != "" {
		cfg.Channels.Feishu.AppSecret = v
	}
	if v := os.Getenv("FEISHU_DOMAIN"); v != "" {
		cfg.Channels.Feishu.Domain = v
	}
	if v := os.Getenv("FEISHU_OPEN_BASE_URL"); v != "" {
		cfg.Channels.Feishu.OpenBaseURL = v
	}
	if v := os.Getenv("TELEGRAM_BOT_TOKEN"); v != "" {
		cfg.Channels.Telegram.Token = v
		cfg.Channels.Telegram.Enabled = true
	}
	if v := os.Getenv("TELEGRAM_ALLOWED_USERS"); v != "" {
		cfg.Channels.Telegram.AllowedUsers = strings.Split(v, ",")
	}
	if v := os.Getenv("DINGTALK_APP_KEY"); v != "" {
		cfg.Channels.DingTalk.AppKey = v
		cfg.Channels.DingTalk.Enabled = true
	}
	if v := os.Getenv("DINGTALK_APP_SECRET"); v != "" {
		cfg.Channels.DingTalk.AppSecret = v
	}
	if v := os.Getenv("DINGTALK_ROBOT_TOKEN"); v != "" {
		cfg.Channels.DingTalk.RobotToken = v
	}
	if v := os.Getenv("WECOM_CORP_ID"); v != "" {
		cfg.Channels.WeCom.CorpID = v
		cfg.Channels.WeCom.Enabled = true
	}
	if v := os.Getenv("WECOM_SECRET"); v != "" {
		cfg.Channels.WeCom.Secret = v
	}
	if v := os.Getenv("WECOM_AGENT_ID"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Channels.WeCom.AgentID = n
		}
	}

	// 备份设置
	if v := os.Getenv("CLAWDBOT_BACKUP_ENABLED"); v != "" {
		cfg.Backup.Enabled = v == "true" || v == "1"
	}
	if v := os.Getenv("CLAWDBOT_BACKUP_MAX_VERSIONS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Backup.MaxVersions = n
		}
	}

	// 心跳
	if v := os.Getenv("CLAWDBOT_HEARTBEAT_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Heartbeat.IntervalMinutes = n
		}
	}
	if v := os.Getenv("CLAWDBOT_QUIET_START"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Heartbeat.QuietStart = n
		}
	}
	if v := os.Getenv("CLAWDBOT_QUIET_END"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Heartbeat.QuietEnd = n
		}
	}
	if v := os.Getenv("CLAWDBOT_HEARTBEAT_ADMIN_CHAT_ID"); v != "" {
		cfg.Heartbeat.AdminChatID = v
	}

	// 安全
	if v := os.Getenv("CLAWDBOT_SKIP_CONFIRMATION"); v != "" {
		cfg.SkipConfirmation = v == "true" || v == "1"
	}

	// 解析 PROVIDER_* 环境变量
	cfg.parseProvidersFromEnv()
}

// parseProvidersFromEnv 从环境变量解析所有 PROVIDER_* 配置
// 格式: PROVIDER_<NAME>_API_KEY, PROVIDER_<NAME>_BASE_URL, PROVIDER_<NAME>_MODEL, PROVIDER_<NAME>_TYPE, PROVIDER_<NAME>_CONTEXT
func (c *Config) parseProvidersFromEnv() {
	c.providers = make(map[string]*Provider)

	// 先尝试新的 PROVIDER_* 格式
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		val := parts[1]

		// 匹配 PROVIDER_XXX_API_KEY
		if !strings.HasPrefix(key, "PROVIDER_") || !strings.HasSuffix(key, "_API_KEY") {
			continue
		}

		// 提取 provider 名称
		name := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(key, "PROVIDER_"), "_API_KEY"))
		if name == "" {
			continue
		}

		prefix := "PROVIDER_" + strings.ToUpper(name)

		p := &Provider{
			Name:       name,
			APIKey:     val,
			BaseURL:    os.Getenv(prefix + "_BASE_URL"),
			Model:      os.Getenv(prefix + "_MODEL"),
			APIType:    os.Getenv(prefix + "_TYPE"),
			MaxContext: 0,
		}

		// 解析上下文窗口
		if ctxStr := os.Getenv(prefix + "_CONTEXT"); ctxStr != "" {
			if n, err := strconv.Atoi(ctxStr); err == nil && n > 0 {
				p.MaxContext = n
			}
		}

		normalizeProvider(p)

		// 只保存有 APIKey 的 provider
		if p.APIKey != "" {
			c.providers[name] = p
		}
	}

	// 向后兼容：如果没有新格式，解析旧格式
	if len(c.providers) == 0 {
		c.parseLegacyProviders()
	}
	// 最后回退：JSON 中的 models.providers（旧配置文件）
	if len(c.providers) == 0 {
		c.parseProvidersFromJSON()
	}
}

// parseLegacyProviders 解析旧的 ENV 格式（向后兼容）
func (c *Config) parseLegacyProviders() {
	providers := []struct {
		envPrefix string
		name      string
		baseURL   string
		model     string
		apiType   string
	}{
		{"MINIMAX", "minimax", "https://api.minimaxi.com/anthropic", "MiniMax-M2.5", "anthropic"},
		{"BAILIAN", "bailian", "https://coding.dashscope.aliyuncs.com/apps/anthropic", "qwen-max", "anthropic"},
		{"KIMI", "kimi", "", "k2p5", "anthropic"},
		{"ANTHROPIC", "anthropic", "", "claude-3-5-sonnet-latest", "anthropic"},
		{"OPENAI", "openai", "https://api.openai.com/v1", "gpt-4o", "openai"},
	}

	for _, prov := range providers {
		apiKey := os.Getenv(prov.envPrefix + "_API_KEY")
		if apiKey == "" {
			continue
		}

		baseURL := os.Getenv(prov.envPrefix + "_BASE_URL")
		if baseURL == "" {
			baseURL = prov.baseURL
		}

		model := os.Getenv(prov.envPrefix + "_MODEL")
		if model == "" {
			model = prov.model
		}

		c.providers[prov.name] = &Provider{
			Name:       prov.name,
			APIKey:     apiKey,
			BaseURL:    baseURL,
			Model:      model,
			APIType:    prov.apiType,
			MaxContext: 0,
		}
		normalizeProvider(c.providers[prov.name])
	}
}

func (c *Config) parseProvidersFromJSON() {
	if c.Models.Providers == nil {
		return
	}
	for name, p := range c.Models.Providers {
		if strings.TrimSpace(p.APIKey) == "" {
			continue
		}
		prov := &Provider{
			Name:       strings.ToLower(strings.TrimSpace(name)),
			BaseURL:    strings.TrimSpace(p.BaseURL),
			APIKey:     strings.TrimSpace(p.APIKey),
			Model:      strings.TrimSpace(p.Model),
			APIType:    strings.TrimSpace(p.API),
			MaxContext: 0,
		}
		normalizeProvider(prov)
		if prov.Name != "" {
			c.providers[prov.Name] = prov
		}
	}
}

func normalizeProvider(p *Provider) {
	p.APIType = strings.ToLower(strings.TrimSpace(p.APIType))
	p.BaseURL = strings.TrimSpace(p.BaseURL)

	// 兼容常见的配置错误：
	// DashScope 的 compatible-mode 是 OpenAI 协议，若误配为 anthropic，自动修正。
	if p.APIType == "anthropic" && strings.Contains(strings.ToLower(p.BaseURL), "compatible-mode") {
		p.APIType = "openai"
	}

	if p.APIType == "" {
		p.APIType = inferAPIType(p.Name, p.BaseURL)
	}
}

func inferAPIType(name, baseURL string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	b := strings.ToLower(strings.TrimSpace(baseURL))

	switch {
	case strings.Contains(b, "compatible-mode"):
		return "openai"
	case strings.Contains(b, "/multimodal-generation/generation"):
		return "openai"
	case strings.Contains(b, "/chat/completions"):
		return "openai"
	case strings.Contains(b, "/anthropic"):
		return "anthropic"
	}

	switch n {
	case "openai", "qwen", "deepseek":
		return "openai"
	default:
		return "anthropic"
	}
}

// GetProvider 根据名称获取 provider
func (c *Config) GetProvider(name string) (*Provider, bool) {
	if c.providers == nil {
		return nil, false
	}
	p, ok := c.providers[strings.ToLower(name)]
	return p, ok
}

// ListProviders 列出所有配置的 provider
func (c *Config) ListProviders() []*Provider {
	if c.providers == nil {
		return nil
	}
	var list []*Provider
	for _, p := range c.providers {
		list = append(list, p)
	}
	// 排序以保证确定性
	sort.Slice(list, func(i, j int) bool {
		return list[i].Name < list[j].Name
	})
	return list
}

// DefaultProvider 返回默认 provider（由 DEFAULT_PROVIDER 指定，或第一个配置的）
func (c *Config) DefaultProvider() (*Provider, bool) {
	if c.providers == nil || len(c.providers) == 0 {
		return nil, false
	}

	// 检查 DEFAULT_PROVIDER
	if name := os.Getenv("DEFAULT_PROVIDER"); name != "" {
		if p, ok := c.GetProvider(name); ok {
			return p, true
		}
	}

	// 如果没有设置默认值，按字母顺序返回第一个
	var keys []string
	for k := range c.providers {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	if len(keys) > 0 {
		return c.providers[keys[0]], true
	}
	return nil, false
}

// ActiveProvider 返回默认 provider（兼容旧接口）
func (c *Config) ActiveProvider() (string, ProviderConfig, error) {
	if p, ok := c.DefaultProvider(); ok {
		return p.Name, ProviderConfig{
			BaseURL: p.BaseURL,
			APIKey:  p.APIKey,
			Model:   p.Model,
			API:     p.APIType,
		}, nil
	}
	return "", ProviderConfig{}, fmt.Errorf("未配置 LLM 提供商：请设置 PROVIDER_XXX_API_KEY 或旧格式如 ANTHROPIC_API_KEY")
}

// AgentByID 根据 ID 返回代理配置。
func (c *Config) AgentByID(id string) (AgentConfig, bool) {
	for _, a := range c.Agents {
		if a.ID == id {
			return a, true
		}
	}
	return AgentConfig{}, false
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}
