package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"goclaw/internal/config"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkdrive "github.com/larksuite/oapi-sdk-go/v3/service/drive/v1"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkwiki "github.com/larksuite/oapi-sdk-go/v3/service/wiki/v2"
)

const (
	feishuDefaultTimeout       = 20 * time.Second
	feishuHTTPRetryAttempts    = 3
	feishuHTTPRetryBaseBackoff = 200 * time.Millisecond
)

// RegisterFeishuTools 注册飞书相关的 Agent 工具。
//
// 当前阶段对齐 OpenClaw 的第一批核心能力：
// - feishu_chat
// - feishu_wiki
// - feishu_drive
// - feishu_doc
// - feishu_bitable
// - feishu_perm
func RegisterFeishuTools(reg *Registry, cfg *config.Config) error {
	if reg == nil {
		return fmt.Errorf("registry 不能为空")
	}
	if cfg == nil {
		return fmt.Errorf("config 不能为空")
	}

	if strings.TrimSpace(cfg.Channels.Feishu.AppID) == "" || strings.TrimSpace(cfg.Channels.Feishu.AppSecret) == "" {
		return nil
	}

	tools := []Tool{
		&FeishuChatTool{Cfg: cfg},
		&FeishuWikiTool{Cfg: cfg},
		&FeishuDriveTool{Cfg: cfg},
		&FeishuDocTool{Cfg: cfg},
		&FeishuBitableTool{Cfg: cfg},
		&FeishuPermTool{Cfg: cfg},
	}
	for _, t := range tools {
		if err := reg.Register(t); err != nil {
			return err
		}
	}
	return nil
}

type feishuToolBase struct {
	Cfg *config.Config
}

func (b *feishuToolBase) newClient() (*lark.Client, error) {
	if b == nil || b.Cfg == nil {
		return nil, fmt.Errorf("飞书配置未初始化")
	}
	fc := b.Cfg.Channels.Feishu
	if strings.TrimSpace(fc.AppID) == "" || strings.TrimSpace(fc.AppSecret) == "" {
		return nil, fmt.Errorf("飞书 appId / appSecret 未配置")
	}

	opts := []lark.ClientOptionFunc{
		lark.WithLogLevel(larkcore.LogLevelWarn),
		lark.WithHttpClient(newFeishuRetryHTTPClient()),
	}
	if strings.TrimSpace(fc.OpenBaseURL) != "" {
		opts = append(opts, lark.WithOpenBaseUrl(strings.TrimSpace(fc.OpenBaseURL)))
	} else if strings.EqualFold(strings.TrimSpace(fc.Domain), "lark") {
		opts = append(opts, lark.WithOpenBaseUrl("https://open.larksuite.com"))
	}

	return lark.NewClient(fc.AppID, fc.AppSecret, opts...), nil
}

type feishuRetryHTTPClient struct {
	base        *http.Client
	maxAttempts int
	baseBackoff time.Duration
}

func newFeishuRetryHTTPClient() *feishuRetryHTTPClient {
	return &feishuRetryHTTPClient{
		base:        http.DefaultClient,
		maxAttempts: feishuHTTPRetryAttempts,
		baseBackoff: feishuHTTPRetryBaseBackoff,
	}
}

func (c *feishuRetryHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if c == nil {
		return http.DefaultClient.Do(req)
	}
	base := c.base
	if base == nil {
		base = http.DefaultClient
	}
	maxAttempts := c.maxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	baseBackoff := c.baseBackoff
	if baseBackoff <= 0 {
		baseBackoff = 100 * time.Millisecond
	}

	bodyBytes, err := snapshotRequestBody(req)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		creq := cloneRequestWithBody(req, bodyBytes)
		resp, err := base.Do(creq)
		if err != nil {
			lastErr = err
			if attempt == maxAttempts || !isRetryableHTTPError(err) {
				return nil, err
			}
			if waitErr := sleepWithContext(req.Context(), retryBackoff(baseBackoff, attempt)); waitErr != nil {
				return nil, waitErr
			}
			continue
		}

		if attempt < maxAttempts && isRetryableStatusCode(resp.StatusCode) {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if waitErr := sleepWithContext(req.Context(), retryBackoff(baseBackoff, attempt)); waitErr != nil {
				return nil, waitErr
			}
			continue
		}
		return resp, nil
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("feishu retry http client: exhausted retries")
}

func snapshotRequestBody(req *http.Request) ([]byte, error) {
	if req == nil || req.Body == nil {
		return nil, nil
	}
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	return bodyBytes, nil
}

func cloneRequestWithBody(req *http.Request, body []byte) *http.Request {
	if req == nil {
		return nil
	}
	clone := req.Clone(req.Context())
	if body != nil {
		clone.Body = io.NopCloser(bytes.NewReader(body))
		clone.ContentLength = int64(len(body))
	}
	return clone
}

func isRetryableStatusCode(status int) bool {
	return status == http.StatusTooManyRequests ||
		status == http.StatusInternalServerError ||
		status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable ||
		status == http.StatusGatewayTimeout
}

func isRetryableHTTPError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, token := range []string{
		"timeout",
		"temporarily unavailable",
		"connection reset",
		"broken pipe",
		"eof",
		"tls handshake timeout",
	} {
		if strings.Contains(msg, token) {
			return true
		}
	}
	return false
}

func retryBackoff(base time.Duration, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	return base * time.Duration(1<<(attempt-1))
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func feishuCallCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), feishuDefaultTimeout)
}

func jsonPretty(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func mustJSON(v any) string {
	out, err := jsonPretty(v)
	if err != nil {
		return fmt.Sprintf(`{"error":"json encode failed: %s"}`, err)
	}
	return out
}

func strPtr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func boolPtr(v *bool) bool {
	if v == nil {
		return false
	}
	return *v
}

func intPtr(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func normalizePageSize(v, fallback, max int) int {
	if v <= 0 {
		return fallback
	}
	if v > max {
		return max
	}
	return v
}

// ---------------------
// feishu_chat
// ---------------------

type FeishuChatTool struct {
	Cfg *config.Config
}

func (t *FeishuChatTool) Name() string { return "feishu_chat" }

func (t *FeishuChatTool) Description() string {
	return `飞书群聊查询工具。
支持 action：
- info: 查询群信息
- members: 查询群成员`
}

func (t *FeishuChatTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["info", "members"],
				"description": "操作类型"
			},
			"chat_id": {
				"type": "string",
				"description": "群 ID (oc_xxx)"
			},
			"page_size": {
				"type": "integer",
				"description": "分页大小，仅 members 生效"
			},
			"page_token": {
				"type": "string",
				"description": "分页 token，仅 members 生效"
			},
			"member_id_type": {
				"type": "string",
				"enum": ["open_id", "user_id", "union_id"],
				"description": "成员 ID 类型，默认 open_id"
			}
		},
		"required": ["action", "chat_id"]
	}`)
}

func (t *FeishuChatTool) Execute(input json.RawMessage, _ *ExecContext) (string, error) {
	var in struct {
		Action       string `json:"action"`
		ChatID       string `json:"chat_id"`
		PageSize     int    `json:"page_size"`
		PageToken    string `json:"page_token"`
		MemberIDType string `json:"member_id_type"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("feishu_chat 参数解析失败: %w", err)
	}
	if strings.TrimSpace(in.Action) == "" {
		return "", fmt.Errorf("feishu_chat: action 不能为空")
	}
	if strings.TrimSpace(in.ChatID) == "" {
		return "", fmt.Errorf("feishu_chat: chat_id 不能为空")
	}

	base := feishuToolBase{Cfg: t.Cfg}
	client, err := base.newClient()
	if err != nil {
		return "", err
	}

	ctx, cancel := feishuCallCtx()
	defer cancel()

	switch in.Action {
	case "info":
		req := larkim.NewGetChatReqBuilder().
			ChatId(in.ChatID).
			UserIdType(larkim.UserIdTypeGetChatOpenId).
			Build()
		resp, err := client.Im.Chat.Get(ctx, req)
		if err != nil {
			return "", fmt.Errorf("feishu_chat.info 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_chat.info 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}

		return mustJSON(map[string]any{
			"chat_id":                  in.ChatID,
			"name":                     strPtr(resp.Data.Name),
			"description":              strPtr(resp.Data.Description),
			"owner_id":                 strPtr(resp.Data.OwnerId),
			"chat_mode":                strPtr(resp.Data.ChatMode),
			"chat_type":                strPtr(resp.Data.ChatType),
			"join_message_visibility":  strPtr(resp.Data.JoinMessageVisibility),
			"leave_message_visibility": strPtr(resp.Data.LeaveMessageVisibility),
			"membership_approval":      strPtr(resp.Data.MembershipApproval),
			"avatar":                   strPtr(resp.Data.Avatar),
		}), nil

	case "members":
		memberIDType := strings.TrimSpace(in.MemberIDType)
		if memberIDType == "" {
			memberIDType = larkim.MemberIdTypeGetChatMembersOpenId
		}
		req := larkim.NewGetChatMembersReqBuilder().
			ChatId(in.ChatID).
			MemberIdType(memberIDType).
			PageSize(normalizePageSize(in.PageSize, 50, 100))
		if strings.TrimSpace(in.PageToken) != "" {
			req.PageToken(in.PageToken)
		}
		resp, err := client.Im.ChatMembers.Get(ctx, req.Build())
		if err != nil {
			return "", fmt.Errorf("feishu_chat.members 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_chat.members 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}

		items := make([]map[string]any, 0, len(resp.Data.Items))
		for _, m := range resp.Data.Items {
			if m == nil {
				continue
			}
			items = append(items, map[string]any{
				"member_id":      strPtr(m.MemberId),
				"name":           strPtr(m.Name),
				"tenant_key":     strPtr(m.TenantKey),
				"member_id_type": strPtr(m.MemberIdType),
			})
		}
		return mustJSON(map[string]any{
			"chat_id":      in.ChatID,
			"has_more":     boolPtr(resp.Data.HasMore),
			"page_token":   strPtr(resp.Data.PageToken),
			"member_total": intPtr(resp.Data.MemberTotal),
			"members":      items,
		}), nil
	default:
		return "", fmt.Errorf("feishu_chat: 未知 action=%q", in.Action)
	}
}

// ---------------------
// feishu_wiki
// ---------------------

type FeishuWikiTool struct {
	Cfg *config.Config
}

func (t *FeishuWikiTool) Name() string { return "feishu_wiki" }

func (t *FeishuWikiTool) Description() string {
	return `飞书知识库工具。
支持 action：
- spaces: 列出知识空间
- nodes: 列出节点
- get: 获取节点
- create: 创建节点
- move: 移动节点
- rename: 重命名节点`
}

func (t *FeishuWikiTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["spaces", "nodes", "get", "create", "move", "rename"],
				"description": "操作类型"
			},
			"space_id": {"type": "string", "description": "知识空间 ID"},
			"parent_node_token": {"type": "string", "description": "父节点 token"},
			"token": {"type": "string", "description": "节点 token（get）"},
			"obj_type": {
				"type": "string",
				"enum": ["doc", "docx", "sheet", "mindnote", "bitable", "file", "slides"],
				"description": "文档类型（create/get 可选）"
			},
			"title": {"type": "string", "description": "标题（create/rename）"},
			"node_token": {"type": "string", "description": "节点 token（move/rename）"},
			"target_space_id": {"type": "string", "description": "目标空间 ID（move 可选）"},
			"target_parent_token": {"type": "string", "description": "目标父节点 token（move 可选）"},
			"page_size": {"type": "integer", "description": "分页大小（spaces/nodes）"},
			"page_token": {"type": "string", "description": "分页 token（spaces/nodes）"}
		},
		"required": ["action"]
	}`)
}

func (t *FeishuWikiTool) Execute(input json.RawMessage, _ *ExecContext) (string, error) {
	var in struct {
		Action            string `json:"action"`
		SpaceID           string `json:"space_id"`
		ParentNodeToken   string `json:"parent_node_token"`
		Token             string `json:"token"`
		ObjType           string `json:"obj_type"`
		Title             string `json:"title"`
		NodeToken         string `json:"node_token"`
		TargetSpaceID     string `json:"target_space_id"`
		TargetParentToken string `json:"target_parent_token"`
		PageSize          int    `json:"page_size"`
		PageToken         string `json:"page_token"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("feishu_wiki 参数解析失败: %w", err)
	}
	if strings.TrimSpace(in.Action) == "" {
		return "", fmt.Errorf("feishu_wiki: action 不能为空")
	}

	base := feishuToolBase{Cfg: t.Cfg}
	client, err := base.newClient()
	if err != nil {
		return "", err
	}

	ctx, cancel := feishuCallCtx()
	defer cancel()

	switch in.Action {
	case "spaces":
		req := larkwiki.NewListSpaceReqBuilder().
			PageSize(normalizePageSize(in.PageSize, 50, 100))
		if strings.TrimSpace(in.PageToken) != "" {
			req.PageToken(in.PageToken)
		}
		resp, err := client.Wiki.Space.List(ctx, req.Build())
		if err != nil {
			return "", fmt.Errorf("feishu_wiki.spaces 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_wiki.spaces 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}

		spaces := make([]map[string]any, 0, len(resp.Data.Items))
		for _, s := range resp.Data.Items {
			if s == nil {
				continue
			}
			spaces = append(spaces, map[string]any{
				"space_id":    strPtr(s.SpaceId),
				"name":        strPtr(s.Name),
				"description": strPtr(s.Description),
				"visibility":  strPtr(s.Visibility),
			})
		}
		return mustJSON(map[string]any{
			"spaces":     spaces,
			"has_more":   boolPtr(resp.Data.HasMore),
			"page_token": strPtr(resp.Data.PageToken),
		}), nil

	case "nodes":
		if strings.TrimSpace(in.SpaceID) == "" {
			return "", fmt.Errorf("feishu_wiki.nodes: space_id 不能为空")
		}
		req := larkwiki.NewListSpaceNodeReqBuilder().
			SpaceId(in.SpaceID).
			PageSize(normalizePageSize(in.PageSize, 50, 100))
		if strings.TrimSpace(in.ParentNodeToken) != "" {
			req.ParentNodeToken(in.ParentNodeToken)
		}
		if strings.TrimSpace(in.PageToken) != "" {
			req.PageToken(in.PageToken)
		}
		resp, err := client.Wiki.SpaceNode.List(ctx, req.Build())
		if err != nil {
			return "", fmt.Errorf("feishu_wiki.nodes 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_wiki.nodes 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}

		nodes := make([]map[string]any, 0, len(resp.Data.Items))
		for _, n := range resp.Data.Items {
			if n == nil {
				continue
			}
			nodes = append(nodes, map[string]any{
				"node_token": strPtr(n.NodeToken),
				"obj_token":  strPtr(n.ObjToken),
				"obj_type":   strPtr(n.ObjType),
				"title":      strPtr(n.Title),
				"has_child":  boolPtr(n.HasChild),
			})
		}
		return mustJSON(map[string]any{
			"space_id":   in.SpaceID,
			"nodes":      nodes,
			"has_more":   boolPtr(resp.Data.HasMore),
			"page_token": strPtr(resp.Data.PageToken),
		}), nil

	case "get":
		if strings.TrimSpace(in.Token) == "" {
			return "", fmt.Errorf("feishu_wiki.get: token 不能为空")
		}
		req := larkwiki.NewGetNodeSpaceReqBuilder().Token(in.Token)
		if strings.TrimSpace(in.ObjType) != "" {
			req.ObjType(in.ObjType)
		}
		resp, err := client.Wiki.Space.GetNode(ctx, req.Build())
		if err != nil {
			return "", fmt.Errorf("feishu_wiki.get 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_wiki.get 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}
		n := resp.Data.Node
		return mustJSON(map[string]any{
			"node_token":        strPtr(n.NodeToken),
			"space_id":          strPtr(n.SpaceId),
			"obj_token":         strPtr(n.ObjToken),
			"obj_type":          strPtr(n.ObjType),
			"title":             strPtr(n.Title),
			"parent_node_token": strPtr(n.ParentNodeToken),
			"has_child":         boolPtr(n.HasChild),
			"creator":           strPtr(n.Creator),
			"create_time":       strPtr(n.NodeCreateTime),
		}), nil

	case "create":
		if strings.TrimSpace(in.SpaceID) == "" {
			return "", fmt.Errorf("feishu_wiki.create: space_id 不能为空")
		}
		if strings.TrimSpace(in.Title) == "" {
			return "", fmt.Errorf("feishu_wiki.create: title 不能为空")
		}
		objType := normalizeWikiObjType(in.ObjType)
		nb := larkwiki.NewNodeBuilder().
			ObjType(objType).
			NodeType(larkwiki.NodeTypeNodeTypeEntity).
			Title(in.Title)
		if strings.TrimSpace(in.ParentNodeToken) != "" {
			nb.ParentNodeToken(in.ParentNodeToken)
		}
		req := larkwiki.NewCreateSpaceNodeReqBuilder().
			SpaceId(in.SpaceID).
			Node(nb.Build()).
			Build()
		resp, err := client.Wiki.SpaceNode.Create(ctx, req)
		if err != nil {
			return "", fmt.Errorf("feishu_wiki.create 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_wiki.create 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}
		n := resp.Data.Node
		return mustJSON(map[string]any{
			"node_token": strPtr(n.NodeToken),
			"obj_token":  strPtr(n.ObjToken),
			"obj_type":   strPtr(n.ObjType),
			"title":      strPtr(n.Title),
		}), nil

	case "move":
		if strings.TrimSpace(in.SpaceID) == "" || strings.TrimSpace(in.NodeToken) == "" {
			return "", fmt.Errorf("feishu_wiki.move: space_id 和 node_token 不能为空")
		}
		body := larkwiki.NewMoveSpaceNodeReqBodyBuilder()
		if strings.TrimSpace(in.TargetSpaceID) != "" {
			body.TargetSpaceId(in.TargetSpaceID)
		}
		if strings.TrimSpace(in.TargetParentToken) != "" {
			body.TargetParentToken(in.TargetParentToken)
		}
		req := larkwiki.NewMoveSpaceNodeReqBuilder().
			SpaceId(in.SpaceID).
			NodeToken(in.NodeToken).
			Body(body.Build()).
			Build()
		resp, err := client.Wiki.SpaceNode.Move(ctx, req)
		if err != nil {
			return "", fmt.Errorf("feishu_wiki.move 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_wiki.move 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}
		nodeToken := ""
		if resp.Data != nil && resp.Data.Node != nil {
			nodeToken = strPtr(resp.Data.Node.NodeToken)
		}
		return mustJSON(map[string]any{
			"success":    true,
			"node_token": nodeToken,
		}), nil

	case "rename":
		if strings.TrimSpace(in.SpaceID) == "" || strings.TrimSpace(in.NodeToken) == "" || strings.TrimSpace(in.Title) == "" {
			return "", fmt.Errorf("feishu_wiki.rename: space_id、node_token、title 不能为空")
		}
		req := larkwiki.NewUpdateTitleSpaceNodeReqBuilder().
			SpaceId(in.SpaceID).
			NodeToken(in.NodeToken).
			Body(larkwiki.NewUpdateTitleSpaceNodeReqBodyBuilder().Title(in.Title).Build()).
			Build()
		resp, err := client.Wiki.SpaceNode.UpdateTitle(ctx, req)
		if err != nil {
			return "", fmt.Errorf("feishu_wiki.rename 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_wiki.rename 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}
		return mustJSON(map[string]any{
			"success":    true,
			"node_token": in.NodeToken,
			"title":      in.Title,
		}), nil

	default:
		return "", fmt.Errorf("feishu_wiki: 未知 action=%q", in.Action)
	}
}

func normalizeWikiObjType(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "doc", "docx", "sheet", "mindnote", "bitable", "file", "slides":
		return v
	default:
		return larkwiki.ObjTypeObjTypeDocx
	}
}

// ---------------------
// feishu_drive
// ---------------------

type FeishuDriveTool struct {
	Cfg *config.Config
}

func (t *FeishuDriveTool) Name() string { return "feishu_drive" }

func (t *FeishuDriveTool) Description() string {
	return `飞书云盘工具。
支持 action：
- list: 列出文件/文件夹
- info: 查询文件信息（按 token）
- create_folder: 创建文件夹
- move: 移动文件
- delete: 删除文件`
}

func (t *FeishuDriveTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["list", "info", "create_folder", "move", "delete"],
				"description": "操作类型"
			},
			"folder_token": {"type": "string", "description": "目录 token（list/create_folder/move）"},
			"file_token": {"type": "string", "description": "文件 token（info/move/delete）"},
			"name": {"type": "string", "description": "文件夹名（create_folder）"},
			"type": {
				"type": "string",
				"enum": ["doc", "docx", "sheet", "bitable", "folder", "file", "mindnote", "slides", "shortcut"],
				"description": "文件类型（move/delete）"
			},
			"page_size": {"type": "integer", "description": "分页大小（list）"},
			"page_token": {"type": "string", "description": "分页 token（list）"}
		},
		"required": ["action"]
	}`)
}

func (t *FeishuDriveTool) Execute(input json.RawMessage, _ *ExecContext) (string, error) {
	var in struct {
		Action      string `json:"action"`
		FolderToken string `json:"folder_token"`
		FileToken   string `json:"file_token"`
		Name        string `json:"name"`
		Type        string `json:"type"`
		PageSize    int    `json:"page_size"`
		PageToken   string `json:"page_token"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("feishu_drive 参数解析失败: %w", err)
	}
	if strings.TrimSpace(in.Action) == "" {
		return "", fmt.Errorf("feishu_drive: action 不能为空")
	}

	base := feishuToolBase{Cfg: t.Cfg}
	client, err := base.newClient()
	if err != nil {
		return "", err
	}

	ctx, cancel := feishuCallCtx()
	defer cancel()

	switch in.Action {
	case "list":
		req := larkdrive.NewListFileReqBuilder().
			PageSize(normalizePageSize(in.PageSize, 50, 200))
		if strings.TrimSpace(in.PageToken) != "" {
			req.PageToken(in.PageToken)
		}
		if folder := strings.TrimSpace(in.FolderToken); folder != "" {
			req.FolderToken(folder)
		}
		resp, err := client.Drive.File.List(ctx, req.Build())
		if err != nil {
			return "", fmt.Errorf("feishu_drive.list 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_drive.list 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}

		files := mapDriveFiles(resp.Data.Files)
		return mustJSON(map[string]any{
			"files":           files,
			"next_page_token": strPtr(resp.Data.NextPageToken),
			"has_more":        boolPtr(resp.Data.HasMore),
		}), nil

	case "info":
		if strings.TrimSpace(in.FileToken) == "" {
			return "", fmt.Errorf("feishu_drive.info: file_token 不能为空")
		}
		files, err := t.findDriveFileByToken(ctx, client, in.FileToken, in.FolderToken)
		if err != nil {
			return "", err
		}
		if files == nil {
			return "", fmt.Errorf("feishu_drive.info: file 不存在或无权限: %s", in.FileToken)
		}
		return mustJSON(files), nil

	case "create_folder":
		if strings.TrimSpace(in.Name) == "" {
			return "", fmt.Errorf("feishu_drive.create_folder: name 不能为空")
		}
		folderToken := strings.TrimSpace(in.FolderToken)
		if folderToken == "" {
			folderToken = "0"
		}
		req := larkdrive.NewCreateFolderFileReqBuilder().
			Body(larkdrive.NewCreateFolderFileReqBodyBuilder().
				Name(in.Name).
				FolderToken(folderToken).
				Build()).
			Build()
		resp, err := client.Drive.File.CreateFolder(ctx, req)
		if err != nil {
			return "", fmt.Errorf("feishu_drive.create_folder 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_drive.create_folder 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}
		return mustJSON(map[string]any{
			"token": strPtr(resp.Data.Token),
			"url":   strPtr(resp.Data.Url),
		}), nil

	case "move":
		if strings.TrimSpace(in.FileToken) == "" || strings.TrimSpace(in.FolderToken) == "" {
			return "", fmt.Errorf("feishu_drive.move: file_token 和 folder_token 不能为空")
		}
		type_ := normalizeDriveType(in.Type, false)
		if type_ == "" {
			return "", fmt.Errorf("feishu_drive.move: type 非法")
		}
		req := larkdrive.NewMoveFileReqBuilder().
			FileToken(in.FileToken).
			Body(larkdrive.NewMoveFileReqBodyBuilder().
				Type(type_).
				FolderToken(in.FolderToken).
				Build()).
			Build()
		resp, err := client.Drive.File.Move(ctx, req)
		if err != nil {
			return "", fmt.Errorf("feishu_drive.move 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_drive.move 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}
		return mustJSON(map[string]any{
			"success": true,
			"task_id": strPtr(resp.Data.TaskId),
		}), nil

	case "delete":
		if strings.TrimSpace(in.FileToken) == "" {
			return "", fmt.Errorf("feishu_drive.delete: file_token 不能为空")
		}
		type_ := normalizeDriveType(in.Type, true)
		if type_ == "" {
			return "", fmt.Errorf("feishu_drive.delete: type 非法")
		}
		req := larkdrive.NewDeleteFileReqBuilder().
			FileToken(in.FileToken).
			Type(type_).
			Build()
		resp, err := client.Drive.File.Delete(ctx, req)
		if err != nil {
			return "", fmt.Errorf("feishu_drive.delete 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_drive.delete 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}
		return mustJSON(map[string]any{
			"success": true,
			"task_id": strPtr(resp.Data.TaskId),
		}), nil

	default:
		return "", fmt.Errorf("feishu_drive: 未知 action=%q", in.Action)
	}
}

func (t *FeishuDriveTool) findDriveFileByToken(ctx context.Context, client *lark.Client, fileToken, folderToken string) (map[string]any, error) {
	req := larkdrive.NewListFileReqBuilder().PageSize(200)
	if strings.TrimSpace(folderToken) != "" {
		req.FolderToken(strings.TrimSpace(folderToken))
	}

	for i := 0; i < 10; i++ {
		resp, err := client.Drive.File.List(ctx, req.Build())
		if err != nil {
			return nil, fmt.Errorf("feishu_drive.info(list) 调用失败: %w", err)
		}
		if !resp.Success() {
			return nil, fmt.Errorf("feishu_drive.info(list) 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}

		for _, f := range resp.Data.Files {
			if f != nil && strPtr(f.Token) == fileToken {
				return map[string]any{
					"token":         strPtr(f.Token),
					"name":          strPtr(f.Name),
					"type":          strPtr(f.Type),
					"parent_token":  strPtr(f.ParentToken),
					"url":           strPtr(f.Url),
					"created_time":  strPtr(f.CreatedTime),
					"modified_time": strPtr(f.ModifiedTime),
					"owner_id":      strPtr(f.OwnerId),
				}, nil
			}
		}

		if !boolPtr(resp.Data.HasMore) || strings.TrimSpace(strPtr(resp.Data.NextPageToken)) == "" {
			break
		}
		req.PageToken(strPtr(resp.Data.NextPageToken))
	}

	return nil, nil
}

func mapDriveFiles(files []*larkdrive.File) []map[string]any {
	res := make([]map[string]any, 0, len(files))
	for _, f := range files {
		if f == nil {
			continue
		}
		res = append(res, map[string]any{
			"token":         strPtr(f.Token),
			"name":          strPtr(f.Name),
			"type":          strPtr(f.Type),
			"url":           strPtr(f.Url),
			"created_time":  strPtr(f.CreatedTime),
			"modified_time": strPtr(f.ModifiedTime),
			"owner_id":      strPtr(f.OwnerId),
		})
	}
	return res
}

func normalizeDriveType(v string, allowShortcut bool) string {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "doc", "docx", "sheet", "bitable", "folder", "file", "mindnote", "slides":
		return v
	case "shortcut":
		if allowShortcut {
			return v
		}
		return ""
	default:
		if allowShortcut {
			return "file"
		}
		return ""
	}
}
