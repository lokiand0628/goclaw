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

	names := []string{"feishu_chat", "feishu_wiki", "feishu_drive", "feishu_doc", "feishu_bitable", "feishu_perm"}
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

func TestValidateDocChildrenRange(t *testing.T) {
	tests := []struct {
		name    string
		start   int
		end     int
		wantErr bool
	}{
		{name: "valid", start: 0, end: 1, wantErr: false},
		{name: "equal", start: 1, end: 1, wantErr: true},
		{name: "reverse", start: 2, end: 1, wantErr: true},
		{name: "negative start", start: -1, end: 1, wantErr: true},
		{name: "negative end", start: 0, end: -1, wantErr: true},
	}
	for _, tc := range tests {
		err := validateDocChildrenRange(tc.start, tc.end)
		if tc.wantErr && err == nil {
			t.Fatalf("%s: expected error, got nil", tc.name)
		}
		if !tc.wantErr && err != nil {
			t.Fatalf("%s: expected nil error, got %v", tc.name, err)
		}
	}
}

func TestValidateBitableFieldType(t *testing.T) {
	if err := validateBitableFieldType(1); err != nil {
		t.Fatalf("validateBitableFieldType(1) should be valid, got %v", err)
	}
	if err := validateBitableFieldType(0); err == nil {
		t.Fatalf("validateBitableFieldType(0) should fail")
	}
	if err := validateBitableFieldType(-1); err == nil {
		t.Fatalf("validateBitableFieldType(-1) should fail")
	}
}

func TestNormalizePermissionFileType(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "docx", want: "docx", wantErr: false},
		{in: " DOC ", want: "doc", wantErr: false},
		{in: "", wantErr: true},
		{in: "unknown", wantErr: true},
	}
	for _, tc := range tests {
		got, err := normalizePermissionFileType(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("normalizePermissionFileType(%q) should fail", tc.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("normalizePermissionFileType(%q) unexpected error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("normalizePermissionFileType(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizePermissionMemberType(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "openid", want: "openid", wantErr: false},
		{in: "open_id", want: "openid", wantErr: false},
		{in: "user_id", want: "user_id", wantErr: false},
		{in: "union_id", want: "union_id", wantErr: false},
		{in: "email", want: "email", wantErr: false},
		{in: "bad_type", wantErr: true},
	}
	for _, tc := range tests {
		got, err := normalizePermissionMemberType(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("normalizePermissionMemberType(%q) should fail", tc.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("normalizePermissionMemberType(%q) unexpected error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("normalizePermissionMemberType(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
