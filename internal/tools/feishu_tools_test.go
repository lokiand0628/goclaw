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

func TestFeishuToolsAllActionsWithMockServer(t *testing.T) {
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
			name:     "chat members",
			tool:     &FeishuChatTool{Cfg: cfg},
			input:    `{"action":"members","chat_id":"oc_test"}`,
			contains: []string{`"member_id": "ou_member_1"`, `"member_total": 1`},
		},
		{
			name:     "wiki spaces",
			tool:     &FeishuWikiTool{Cfg: cfg},
			input:    `{"action":"spaces"}`,
			contains: []string{`"space_id": "spc_1"`, `"name": "Mock Space"`},
		},
		{
			name:     "wiki nodes",
			tool:     &FeishuWikiTool{Cfg: cfg},
			input:    `{"action":"nodes","space_id":"spc_1"}`,
			contains: []string{`"node_token": "n_1"`, `"obj_token": "doc_1"`},
		},
		{
			name:     "wiki get",
			tool:     &FeishuWikiTool{Cfg: cfg},
			input:    `{"action":"get","token":"n_1"}`,
			contains: []string{`"node_token": "n_1"`, `"title": "Mock Node"`},
		},
		{
			name:     "wiki create",
			tool:     &FeishuWikiTool{Cfg: cfg},
			input:    `{"action":"create","space_id":"spc_1","title":"New Node"}`,
			contains: []string{`"node_token": "n_created"`, `"title": "New Node"`},
		},
		{
			name:     "wiki move",
			tool:     &FeishuWikiTool{Cfg: cfg},
			input:    `{"action":"move","space_id":"spc_1","node_token":"n_1","target_space_id":"spc_2","target_parent_token":"p_2"}`,
			contains: []string{`"success": true`, `"node_token": "n_1"`},
		},
		{
			name:     "wiki rename",
			tool:     &FeishuWikiTool{Cfg: cfg},
			input:    `{"action":"rename","space_id":"spc_1","node_token":"n_1","title":"Renamed Node"}`,
			contains: []string{`"success": true`, `"title": "Renamed Node"`},
		},
		{
			name:     "drive list",
			tool:     &FeishuDriveTool{Cfg: cfg},
			input:    `{"action":"list"}`,
			contains: []string{`"token": "fil_1"`, `"name": "Mock File"`},
		},
		{
			name:     "drive info",
			tool:     &FeishuDriveTool{Cfg: cfg},
			input:    `{"action":"info","file_token":"fil_1"}`,
			contains: []string{`"token": "fil_1"`, `"name": "Mock File"`},
		},
		{
			name:     "drive create folder",
			tool:     &FeishuDriveTool{Cfg: cfg},
			input:    `{"action":"create_folder","folder_token":"0","name":"New Folder"}`,
			contains: []string{`"token": "fld_1"`, `"url": "https://example.com/folder"`},
		},
		{
			name:     "drive move",
			tool:     &FeishuDriveTool{Cfg: cfg},
			input:    `{"action":"move","file_token":"fil_1","folder_token":"fld_1","type":"file"}`,
			contains: []string{`"success": true`, `"task_id": "task_move_1"`},
		},
		{
			name:     "drive delete",
			tool:     &FeishuDriveTool{Cfg: cfg},
			input:    `{"action":"delete","file_token":"fil_1","type":"file"}`,
			contains: []string{`"success": true`, `"task_id": "task_del_1"`},
		},
		{
			name:     "doc create",
			tool:     &FeishuDocTool{Cfg: cfg},
			input:    `{"action":"create","title":"Mock New Doc","folder_token":"fld_1"}`,
			contains: []string{`"document_id": "d_created"`, `"title": "Mock New Doc"`},
		},
		{
			name:     "doc read",
			tool:     &FeishuDocTool{Cfg: cfg},
			input:    `{"action":"read","document_id":"d1"}`,
			contains: []string{`"document_id": "d1"`, `"raw_content": "mock raw content"`},
		},
		{
			name:     "doc get block",
			tool:     &FeishuDocTool{Cfg: cfg},
			input:    `{"action":"get_block","document_id":"d1","block_id":"b1"}`,
			contains: []string{`"block_id": "b1"`, `"text": "mock block text"`},
		},
		{
			name:     "doc list children",
			tool:     &FeishuDocTool{Cfg: cfg},
			input:    `{"action":"list_children","document_id":"d1","parent_block_id":"b_parent"}`,
			contains: []string{`"parent_block_id": "b_parent"`, `"block_id": "b_child_1"`},
		},
		{
			name:     "doc append text",
			tool:     &FeishuDocTool{Cfg: cfg},
			input:    `{"action":"append_text","document_id":"d1","parent_block_id":"b_parent","text":"append text"}`,
			contains: []string{`"document_revision_id": 3`, `"block_id": "b_appended_1"`},
		},
		{
			name:     "doc update text",
			tool:     &FeishuDocTool{Cfg: cfg},
			input:    `{"action":"update_text","document_id":"d1","block_id":"b1","text":"updated text"}`,
			contains: []string{`"block_id": "b1"`, `"document_revision_id": 4`},
		},
		{
			name:     "doc delete children range",
			tool:     &FeishuDocTool{Cfg: cfg},
			input:    `{"action":"delete_children_range","document_id":"d1","parent_block_id":"b_parent","start_index":0,"end_index":1}`,
			contains: []string{`"success": true`, `"document_revision_id": 5`},
		},
		{
			name:     "bitable meta",
			tool:     &FeishuBitableTool{Cfg: cfg},
			input:    `{"action":"get_meta","app_token":"app1"}`,
			contains: []string{`"app_token": "app1"`, `"name": "Mock App"`},
		},
		{
			name:     "bitable create app",
			tool:     &FeishuBitableTool{Cfg: cfg},
			input:    `{"action":"create_app","name":"Mock New App","folder_token":"fld_1","time_zone":"Asia/Shanghai"}`,
			contains: []string{`"app_token": "app_created"`, `"name": "Mock New App"`},
		},
		{
			name:     "bitable list tables",
			tool:     &FeishuBitableTool{Cfg: cfg},
			input:    `{"action":"list_tables","app_token":"app1"}`,
			contains: []string{`"table_id": "tbl1"`, `"name": "Mock Table"`},
		},
		{
			name:     "bitable list fields",
			tool:     &FeishuBitableTool{Cfg: cfg},
			input:    `{"action":"list_fields","app_token":"app1","table_id":"tbl1"}`,
			contains: []string{`"field_id": "fld1"`, `"field_name": "Name"`},
		},
		{
			name:     "bitable create field",
			tool:     &FeishuBitableTool{Cfg: cfg},
			input:    `{"action":"create_field","app_token":"app1","table_id":"tbl1","field_name":"Score","field_type":2}`,
			contains: []string{`"field_id": "fld_created"`, `"field_name": "Score"`},
		},
		{
			name:     "bitable list records",
			tool:     &FeishuBitableTool{Cfg: cfg},
			input:    `{"action":"list_records","app_token":"app1","table_id":"tbl1"}`,
			contains: []string{`"record_id": "rec1"`, `"Name": "Alice"`},
		},
		{
			name:     "bitable get record",
			tool:     &FeishuBitableTool{Cfg: cfg},
			input:    `{"action":"get_record","app_token":"app1","table_id":"tbl1","record_id":"rec1"}`,
			contains: []string{`"record_id": "rec1"`, `"Name": "Alice"`},
		},
		{
			name:     "bitable create record",
			tool:     &FeishuBitableTool{Cfg: cfg},
			input:    `{"action":"create_record","app_token":"app1","table_id":"tbl1","fields":{"Name":"Bob"}}`,
			contains: []string{`"record_id": "rec_created"`, `"Name": "Bob"`},
		},
		{
			name:     "bitable update record",
			tool:     &FeishuBitableTool{Cfg: cfg},
			input:    `{"action":"update_record","app_token":"app1","table_id":"tbl1","record_id":"rec1","fields":{"Name":"Charlie"}}`,
			contains: []string{`"record_id": "rec1"`, `"Name": "Charlie"`},
		},
		{
			name:     "perm list",
			tool:     &FeishuPermTool{Cfg: cfg},
			input:    `{"action":"list","token":"doc1","file_type":"doc"}`,
			contains: []string{`"member_id": "ou_123"`, `"perm": "view"`},
		},
		{
			name:     "perm add",
			tool:     &FeishuPermTool{Cfg: cfg},
			input:    `{"action":"add","token":"doc1","file_type":"doc","member_type":"open_id","member_id":"ou_123","perm":"edit"}`,
			contains: []string{`"success": true`, `"perm": "edit"`},
		},
		{
			name:     "perm remove",
			tool:     &FeishuPermTool{Cfg: cfg},
			input:    `{"action":"remove","token":"doc1","file_type":"doc","member_type":"open_id","member_id":"ou_123"}`,
			contains: []string{`"success": true`, `"member_id": "ou_123"`},
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
		case "/open-apis/im/v1/chats/oc_test/members":
			writeJSON(w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"items": []map[string]any{
						{
							"member_id":      "ou_member_1",
							"name":           "Member One",
							"tenant_key":     "t_mock",
							"member_id_type": "open_id",
						},
					},
					"has_more":     false,
					"page_token":   "",
					"member_total": 1,
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
		case "/open-apis/wiki/v2/spaces/spc_1/nodes":
			if r.Method == http.MethodGet {
				writeJSON(w, map[string]any{
					"code": 0,
					"msg":  "ok",
					"data": map[string]any{
						"items": []map[string]any{
							{
								"node_token": "n_1",
								"obj_token":  "doc_1",
								"obj_type":   "docx",
								"title":      "Mock Node",
								"has_child":  false,
							},
						},
						"has_more":   false,
						"page_token": "",
					},
				})
				return
			}
			writeJSON(w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"node": map[string]any{
						"node_token": "n_created",
						"obj_token":  "doc_created",
						"obj_type":   "docx",
						"title":      "New Node",
					},
				},
			})
			return
		case "/open-apis/wiki/v2/spaces/get_node":
			writeJSON(w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"node": map[string]any{
						"node_token":        "n_1",
						"space_id":          "spc_1",
						"obj_token":         "doc_1",
						"obj_type":          "docx",
						"title":             "Mock Node",
						"parent_node_token": "p_1",
						"has_child":         false,
						"creator":           "ou_owner",
						"node_create_time":  "1700000000",
					},
				},
			})
			return
		case "/open-apis/wiki/v2/spaces/spc_1/nodes/n_1/move":
			writeJSON(w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"node": map[string]any{
						"node_token": "n_1",
					},
				},
			})
			return
		case "/open-apis/wiki/v2/spaces/spc_1/nodes/n_1/update_title":
			writeJSON(w, map[string]any{
				"code": 0,
				"msg":  "ok",
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
		case "/open-apis/drive/v1/files/create_folder":
			writeJSON(w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"token": "fld_1",
					"url":   "https://example.com/folder",
				},
			})
			return
		case "/open-apis/drive/v1/files/fil_1/move":
			writeJSON(w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"task_id": "task_move_1",
				},
			})
			return
		case "/open-apis/drive/v1/files/fil_1":
			writeJSON(w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"task_id": "task_del_1",
				},
			})
			return
		case "/open-apis/docx/v1/documents":
			writeJSON(w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"document": map[string]any{
						"document_id": "d_created",
						"revision_id": 1,
						"title":       "Mock New Doc",
					},
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
		case "/open-apis/docx/v1/documents/d1/blocks/b1":
			writeJSON(w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"block": map[string]any{
						"block_id":   "b1",
						"parent_id":  "b_parent",
						"children":   []string{},
						"block_type": 2,
						"text": map[string]any{
							"elements": []map[string]any{
								{
									"text_run": map[string]any{
										"content": "mock block text",
									},
								},
							},
						},
					},
					"document_revision_id": 4,
				},
			})
			return
		case "/open-apis/docx/v1/documents/d1/blocks/b_parent/children":
			if r.Method == http.MethodGet {
				writeJSON(w, map[string]any{
					"code": 0,
					"msg":  "ok",
					"data": map[string]any{
						"items": []map[string]any{
							{
								"block_id":   "b_child_1",
								"parent_id":  "b_parent",
								"children":   []string{},
								"block_type": 2,
								"text": map[string]any{
									"elements": []map[string]any{
										{
											"text_run": map[string]any{
												"content": "child text",
											},
										},
									},
								},
							},
						},
						"page_token": "",
						"has_more":   false,
					},
				})
				return
			}
			writeJSON(w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"children": []map[string]any{
						{
							"block_id":   "b_appended_1",
							"parent_id":  "b_parent",
							"children":   []string{},
							"block_type": 2,
							"text": map[string]any{
								"elements": []map[string]any{
									{
										"text_run": map[string]any{
											"content": "append text",
										},
									},
								},
							},
						},
					},
					"document_revision_id": 3,
				},
			})
			return
		case "/open-apis/docx/v1/documents/d1/blocks/b_parent/children/batch_delete":
			writeJSON(w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"document_revision_id": 5,
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
		case "/open-apis/bitable/v1/apps":
			writeJSON(w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"app": map[string]any{
						"app_token":        "app_created",
						"name":             "Mock New App",
						"revision":         1,
						"folder_token":     "fld_1",
						"url":              "https://example.com/bitable",
						"default_table_id": "tbl1",
						"time_zone":        "Asia/Shanghai",
					},
				},
			})
			return
		case "/open-apis/bitable/v1/apps/app1/tables":
			writeJSON(w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"has_more":   false,
					"page_token": "",
					"total":      1,
					"items": []map[string]any{
						{
							"table_id": "tbl1",
							"name":     "Mock Table",
							"revision": 1,
						},
					},
				},
			})
			return
		case "/open-apis/bitable/v1/apps/app1/tables/tbl1/fields":
			if r.Method == http.MethodGet {
				writeJSON(w, map[string]any{
					"code": 0,
					"msg":  "ok",
					"data": map[string]any{
						"has_more":   false,
						"page_token": "",
						"total":      1,
						"items": []map[string]any{
							{
								"field_id":   "fld1",
								"field_name": "Name",
								"type":       1,
								"ui_type":    "Text",
								"is_primary": true,
								"is_hidden":  false,
							},
						},
					},
				})
				return
			}
			writeJSON(w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"field": map[string]any{
						"field_id":   "fld_created",
						"field_name": "Score",
						"type":       2,
						"ui_type":    "Number",
						"is_primary": false,
						"is_hidden":  false,
					},
				},
			})
			return
		case "/open-apis/bitable/v1/apps/app1/tables/tbl1/records":
			if r.Method == http.MethodGet {
				writeJSON(w, map[string]any{
					"code": 0,
					"msg":  "ok",
					"data": map[string]any{
						"has_more":   false,
						"page_token": "",
						"total":      1,
						"items": []map[string]any{
							{
								"record_id": "rec1",
								"fields": map[string]any{
									"Name": "Alice",
								},
								"created_time":       1700000000000,
								"last_modified_time": 1700000000000,
							},
						},
					},
				})
				return
			}
			writeJSON(w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"record": map[string]any{
						"record_id": "rec_created",
						"fields": map[string]any{
							"Name": "Bob",
						},
						"created_time":       1700000000001,
						"last_modified_time": 1700000000001,
					},
				},
			})
			return
		case "/open-apis/bitable/v1/apps/app1/tables/tbl1/records/rec1":
			if r.Method == http.MethodGet {
				writeJSON(w, map[string]any{
					"code": 0,
					"msg":  "ok",
					"data": map[string]any{
						"record": map[string]any{
							"record_id": "rec1",
							"fields": map[string]any{
								"Name": "Alice",
							},
							"created_time":       1700000000000,
							"last_modified_time": 1700000000000,
						},
					},
				})
				return
			}
			writeJSON(w, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"record": map[string]any{
						"record_id": "rec1",
						"fields": map[string]any{
							"Name": "Charlie",
						},
						"created_time":       1700000000000,
						"last_modified_time": 1700000000002,
					},
				},
			})
			return

		case "/open-apis/drive/v1/permissions/doc1/members":
			if r.Method != http.MethodGet {
				writeJSON(w, map[string]any{
					"code": 0,
					"msg":  "ok",
					"data": map[string]any{
						"member": map[string]any{
							"member_type": "openid",
							"member_id":   "ou_123",
							"perm":        "edit",
							"perm_type":   "container",
							"type":        "user",
						},
					},
				})
				return
			}
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
		case "/open-apis/drive/v1/permissions/doc1/members/ou_123":
			writeJSON(w, map[string]any{
				"code": 0,
				"msg":  "ok",
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
