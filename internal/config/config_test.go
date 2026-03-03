package config

import "testing"

func TestInferAPIType(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{"qwen", "https://dashscope.aliyuncs.com/compatible-mode/v1", "openai"},
		{"custom", "https://example.com/v1/chat/completions", "openai"},
		{"kimi", "https://api.kimi.com/coding", "anthropic"},
		{"openai", "", "openai"},
	}

	for _, tt := range tests {
		got := inferAPIType(tt.name, tt.baseURL)
		if got != tt.want {
			t.Fatalf("inferAPIType(%q, %q)=%q, want %q", tt.name, tt.baseURL, got, tt.want)
		}
	}
}

func TestNormalizeProviderCompatibleModeFixesWrongType(t *testing.T) {
	p := &Provider{
		Name:    "qwen",
		BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
		APIType: "anthropic",
	}

	normalizeProvider(p)
	if p.APIType != "openai" {
		t.Fatalf("normalizeProvider should force openai for compatible-mode, got %q", p.APIType)
	}
}

func TestParseLegacyProvidersIncludesOpenAI(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("OPENAI_BASE_URL", "https://api.openai.com/v1")
	t.Setenv("OPENAI_MODEL", "gpt-4o")

	c := &Config{providers: make(map[string]*Provider)}
	c.parseLegacyProviders()

	p, ok := c.GetProvider("openai")
	if !ok {
		t.Fatal("expected openai provider from legacy env")
	}
	if p.APIType != "openai" {
		t.Fatalf("expected openai type, got %q", p.APIType)
	}
}

func TestOverlayEnvFeishuOpenBaseURL(t *testing.T) {
	t.Setenv("FEISHU_APP_ID", "cli_test")
	t.Setenv("FEISHU_APP_SECRET", "sec_test")
	t.Setenv("FEISHU_OPEN_BASE_URL", "http://127.0.0.1:19090")

	cfg := defaults()
	overlayEnv(cfg)

	if cfg.Channels.Feishu.OpenBaseURL != "http://127.0.0.1:19090" {
		t.Fatalf("FEISHU_OPEN_BASE_URL not applied, got %q", cfg.Channels.Feishu.OpenBaseURL)
	}
}
