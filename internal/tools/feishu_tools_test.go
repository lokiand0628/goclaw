package tools

import (
	"testing"

	"goclaw/internal/config"
)

func TestNormalizeWikiObjType(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"doc", "doc"},
		{"DOCX", "docx"},
		{" sheet ", "sheet"},
		{"unknown", "docx"},
		{"", "docx"},
	}

	for _, tc := range tests {
		got := normalizeWikiObjType(tc.in)
		if got != tc.want {
			t.Fatalf("normalizeWikiObjType(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeDriveType(t *testing.T) {
	tests := []struct {
		name          string
		in            string
		allowShortcut bool
		want          string
	}{
		{name: "normal", in: "docx", allowShortcut: false, want: "docx"},
		{name: "trim lowercase", in: "  FILE ", allowShortcut: false, want: "file"},
		{name: "shortcut disallowed", in: "shortcut", allowShortcut: false, want: ""},
		{name: "shortcut allowed", in: "shortcut", allowShortcut: true, want: "shortcut"},
		{name: "fallback when allow shortcut", in: "unknown", allowShortcut: true, want: "file"},
		{name: "unknown no shortcut", in: "unknown", allowShortcut: false, want: ""},
	}

	for _, tc := range tests {
		got := normalizeDriveType(tc.in, tc.allowShortcut)
		if got != tc.want {
			t.Fatalf("%s: normalizeDriveType(%q, %v) = %q, want %q", tc.name, tc.in, tc.allowShortcut, got, tc.want)
		}
	}
}

func TestRegisterFeishuTools(t *testing.T) {
	reg := NewRegistry()
	cfg := &config.Config{}
	cfg.Channels.Feishu = config.FeishuConfig{
		AppID:     "cli_xxx",
		AppSecret: "sec_xxx",
	}

	if err := RegisterFeishuTools(reg, cfg); err != nil {
		t.Fatalf("RegisterFeishuTools error: %v", err)
	}

	names := []string{"feishu_chat", "feishu_wiki", "feishu_drive"}
	for _, name := range names {
		if _, ok := reg.Get(name); !ok {
			t.Fatalf("tool %q should be registered", name)
		}
	}
}

func TestRegisterFeishuToolsSkipWhenNoCredential(t *testing.T) {
	reg := NewRegistry()
	cfg := &config.Config{}

	if err := RegisterFeishuTools(reg, cfg); err != nil {
		t.Fatalf("RegisterFeishuTools error: %v", err)
	}

	if _, ok := reg.Get("feishu_chat"); ok {
		t.Fatalf("feishu_chat should not be registered without credential")
	}
}
