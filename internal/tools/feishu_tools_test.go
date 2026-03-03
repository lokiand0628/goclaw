package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

func TestFeishuToolsSmokeWithMockServer(t *testing.T) {
	server := newMockFeishuServer(t, nil)
	defer server.Close()

	cfg := newMockFeishuConfig(server.URL)
	cases := []struct {
		name     string
		tool     Tool
		input    string
		contains []string
	}{
		{
			name:     "chat info",
			tool:     &FeishuChatTool{Cfg: cfg},
			input:    `{"action":"info","chat_id":"oc_test"}`,
			contains: []string{`"chat_id": "oc_test"`, `"name": "Mock Chat"`},
		},
		{
			name:     "wiki spaces",
			tool:     &FeishuWikiTool{Cfg: cfg},
			input:    `{"action":"spaces"}`,
			contains: []string{`"space_id": "spc_1"`, `"name": "Mock Space"`},
		},
		{
			name:     "drive list",
			tool:     &FeishuDriveTool{Cfg: cfg},
			input:    `{"action":"list"}`,
			contains: []string{`"token": "fil_1"`, `"name": "Mock File"`},
		},
		{
			name:     "doc read",
			tool:     &FeishuDocTool{Cfg: cfg},
			input:    `{"action":"read","document_id":"d1"}`,
			contains: []string{`"document_id": "d1"`, `"raw_content": "mock raw content"`},
		},
		{
			name:     "bitable meta",
			tool:     &FeishuBitableTool{Cfg: cfg},
			input:    `{"action":"get_meta","app_token":"app1"}`,
			contains: []string{`"app_token": "app1"`, `"name": "Mock App"`},
		},
		{
			name:     "perm list",
			tool:     &FeishuPermTool{Cfg: cfg},
			input:    `{"action":"list","token":"doc1","file_type":"doc"}`,
			contains: []string{`"member_id": "ou_123"`, `"perm": "view"`},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			out, err := tc.tool.Execute(json.RawMessage(tc.input), nil)
			if err != nil {
				t.Fatalf("execute failed: %v", err)
			}
			for _, want := range tc.contains {
				if !strings.Contains(out, want) {
					t.Fatalf("output should contain %q, got: %s", want, out)
				}
			}
		})
	}
}

func TestFeishuRetryHTTPClientOn502(t *testing.T) {
	var chatRetryHits int32
	server := newMockFeishuServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/open-apis/im/v1/chats/oc_retry" {
			if atomic.AddInt32(&chatRetryHits, 1) == 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(`{"code":500,"msg":"temporary bad gateway"}`))
				return true
			}
		}
		return false
	})
	defer server.Close()

	cfg := newMockFeishuConfig(server.URL)
	tool := &FeishuChatTool{Cfg: cfg}
	out, err := tool.Execute(json.RawMessage(`{"action":"info","chat_id":"oc_retry"}`), nil)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if atomic.LoadInt32(&chatRetryHits) < 2 {
		t.Fatalf("expected at least 2 attempts, got %d", atomic.LoadInt32(&chatRetryHits))
	}
	if !strings.Contains(out, `"chat_id": "oc_retry"`) {
		t.Fatalf("unexpected output: %s", out)
	}
}

func newMockFeishuConfig(baseURL string) *config.Config {
	cfg := &config.Config{}
	cfg.Channels.Feishu = config.FeishuConfig{
		AppID:       "cli_mock",
		AppSecret:   "sec_mock",
		OpenBaseURL: baseURL,
	}
	return cfg
}

func newMockFeishuServer(t *testing.T, override func(http.ResponseWriter, *http.Request) bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if override != nil && override(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			writeJSON(w, map[string]any{
				"code":                0,
				"msg":                 "ok",
				"tenant_access_token": "t_mock",
				"expire":              7200,
			})
			return

		case "/open-apis/im/v1/chats/oc_test", "/open-apis/im/v1/chats/oc_retry":
			chatID := "oc_test"
			if strings.HasSuffix(r.URL.Path, "/oc_retry") {
				chatID = "oc_retry"
			}
			writeJSON(w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"chat_id":                  chatID,
					"name":                     "Mock Chat",
					"description":              "Mock Desc",
					"owner_id":                 "ou_owner",
					"chat_mode":                "group",
					"chat_type":                "private",
					"join_message_visibility":  "all",
					"leave_message_visibility": "all",
					"membership_approval":      "no",
					"avatar":                   "https://example.com/avatar.png",
				},
			})
			return

		case "/open-apis/wiki/v2/spaces":
			writeJSON(w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"items": []map[string]any{
						{
							"space_id":    "spc_1",
							"name":        "Mock Space",
							"description": "mock",
							"visibility":  "private",
						},
					},
					"has_more":   false,
					"page_token": "",
				},
			})
			return

		case "/open-apis/drive/v1/files":
			writeJSON(w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"files": []map[string]any{
						{
							"token":         "fil_1",
							"name":          "Mock File",
							"type":          "file",
							"url":           "https://example.com/file",
							"created_time":  "1",
							"modified_time": "2",
							"owner_id":      "ou_owner",
						},
					},
					"next_page_token": "",
					"has_more":        false,
				},
			})
			return

		case "/open-apis/docx/v1/documents/d1":
			writeJSON(w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"document": map[string]any{
						"document_id": "d1",
						"revision_id": 1,
						"title":       "Mock Doc",
					},
				},
			})
			return

		case "/open-apis/docx/v1/documents/d1/raw_content":
			writeJSON(w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"content": "mock raw content",
				},
			})
			return

		case "/open-apis/bitable/v1/apps/app1":
			writeJSON(w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"app": map[string]any{
						"app_token":       "app1",
						"name":            "Mock App",
						"revision":        1,
						"is_advanced":     false,
						"time_zone":       "Asia/Shanghai",
						"formula_type":    0,
						"advance_version": "v1",
					},
				},
			})
			return

		case "/open-apis/drive/v1/permissions/doc1/members":
			writeJSON(w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"items": []map[string]any{
						{
							"member_type": "openid",
							"member_id":   "ou_123",
							"perm":        "view",
							"perm_type":   "container",
							"type":        "user",
							"name":        "Mock User",
							"avatar":      "",
						},
					},
				},
			})
			return
		}

		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, map[string]any{
			"code": 404,
			"msg":  "not mocked: " + r.URL.Path,
		})
	}))
}

func writeJSON(w http.ResponseWriter, payload any) {
	enc := json.NewEncoder(w)
	_ = enc.Encode(payload)
}
