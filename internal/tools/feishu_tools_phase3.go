package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"goclaw/internal/config"

	larkbitable "github.com/larksuite/oapi-sdk-go/v3/service/bitable/v1"
	larkdocx "github.com/larksuite/oapi-sdk-go/v3/service/docx/v1"
	larkdrive "github.com/larksuite/oapi-sdk-go/v3/service/drive/v1"
)

const (
	docxBlockTypeText = 2
)

// ---------------------
// feishu_doc
// ---------------------

type FeishuDocTool struct {
	Cfg *config.Config
}

func (t *FeishuDocTool) Name() string { return "feishu_doc" }

func (t *FeishuDocTool) Description() string {
	return `飞书文档（Docx）工具。
支持 action：
- create: 创建文档
- read: 读取文档元信息与纯文本
- get_block: 获取指定 Block
- list_children: 列出父 Block 子节点
- append_text: 在父 Block 下追加文本子节点
- update_text: 更新指定 Block 文本
- delete_children_range: 批量删除父 Block 指定区间子节点`
}

func (t *FeishuDocTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["create", "read", "get_block", "list_children", "append_text", "update_text", "delete_children_range"],
				"description": "操作类型"
			},
			"document_id": {"type": "string", "description": "文档 ID"},
			"title": {"type": "string", "description": "文档标题（create）"},
			"folder_token": {"type": "string", "description": "目录 token（create 可选）"},
			"block_id": {"type": "string", "description": "Block ID（get_block/update_text）"},
			"parent_block_id": {"type": "string", "description": "父 Block ID（list_children/append_text/delete_children_range）"},
			"text": {"type": "string", "description": "文本内容（append_text/update_text）"},
			"page_size": {"type": "integer", "description": "分页大小（list_children）"},
			"page_token": {"type": "string", "description": "分页 token（list_children）"},
			"index": {"type": "integer", "description": "插入位置（append_text，可选）"},
			"start_index": {"type": "integer", "description": "删除起始索引（delete_children_range）"},
			"end_index": {"type": "integer", "description": "删除结束索引，右开区间（delete_children_range）"}
		},
		"required": ["action"]
	}`)
}

func (t *FeishuDocTool) Execute(input json.RawMessage, _ *ExecContext) (string, error) {
	var in struct {
		Action        string `json:"action"`
		DocumentID    string `json:"document_id"`
		Title         string `json:"title"`
		FolderToken   string `json:"folder_token"`
		BlockID       string `json:"block_id"`
		ParentBlockID string `json:"parent_block_id"`
		Text          string `json:"text"`
		PageSize      int    `json:"page_size"`
		PageToken     string `json:"page_token"`
		Index         *int   `json:"index"`
		StartIndex    *int   `json:"start_index"`
		EndIndex      *int   `json:"end_index"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("feishu_doc 参数解析失败: %w", err)
	}
	if strings.TrimSpace(in.Action) == "" {
		return "", fmt.Errorf("feishu_doc: action 不能为空")
	}

	base := feishuToolBase{Cfg: t.Cfg}
	client, err := base.newClient()
	if err != nil {
		return "", err
	}

	ctx, cancel := feishuCallCtx()
	defer cancel()

	switch in.Action {
	case "create":
		title := strings.TrimSpace(in.Title)
		if title == "" {
			return "", fmt.Errorf("feishu_doc.create: title 不能为空")
		}
		bodyBuilder := larkdocx.NewCreateDocumentReqBodyBuilder().Title(title)
		if folder := strings.TrimSpace(in.FolderToken); folder != "" {
			bodyBuilder.FolderToken(folder)
		}
		req := larkdocx.NewCreateDocumentReqBuilder().Body(bodyBuilder.Build()).Build()
		resp, err := client.Docx.Document.Create(ctx, req)
		if err != nil {
			return "", fmt.Errorf("feishu_doc.create 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_doc.create 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}

		var doc *larkdocx.Document
		if resp.Data != nil {
			doc = resp.Data.Document
		}
		return mustJSON(map[string]any{
			"document": mapDocDocument(doc),
		}), nil

	case "read":
		documentID := strings.TrimSpace(in.DocumentID)
		if documentID == "" {
			return "", fmt.Errorf("feishu_doc.read: document_id 不能为空")
		}
		metaResp, err := client.Docx.Document.Get(ctx, larkdocx.NewGetDocumentReqBuilder().DocumentId(documentID).Build())
		if err != nil {
			return "", fmt.Errorf("feishu_doc.read(get) 调用失败: %w", err)
		}
		if !metaResp.Success() {
			return "", fmt.Errorf("feishu_doc.read(get) 失败: code=%d msg=%s", metaResp.Code, metaResp.Msg)
		}
		rawResp, err := client.Docx.Document.RawContent(ctx, larkdocx.NewRawContentDocumentReqBuilder().DocumentId(documentID).Build())
		if err != nil {
			return "", fmt.Errorf("feishu_doc.read(raw) 调用失败: %w", err)
		}
		if !rawResp.Success() {
			return "", fmt.Errorf("feishu_doc.read(raw) 失败: code=%d msg=%s", rawResp.Code, rawResp.Msg)
		}

		var doc *larkdocx.Document
		if metaResp.Data != nil {
			doc = metaResp.Data.Document
		}
		raw := ""
		if rawResp.Data != nil {
			raw = strPtr(rawResp.Data.Content)
		}

		return mustJSON(map[string]any{
			"document":    mapDocDocument(doc),
			"raw_content": raw,
		}), nil

	case "get_block":
		documentID := strings.TrimSpace(in.DocumentID)
		blockID := strings.TrimSpace(in.BlockID)
		if documentID == "" || blockID == "" {
			return "", fmt.Errorf("feishu_doc.get_block: document_id 和 block_id 不能为空")
		}
		req := larkdocx.NewGetDocumentBlockReqBuilder().
			DocumentId(documentID).
			BlockId(blockID).
			Build()
		resp, err := client.Docx.DocumentBlock.Get(ctx, req)
		if err != nil {
			return "", fmt.Errorf("feishu_doc.get_block 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_doc.get_block 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}

		var block *larkdocx.Block
		if resp.Data != nil {
			block = resp.Data.Block
		}
		return mustJSON(map[string]any{
			"block": mapDocBlock(block),
		}), nil

	case "list_children":
		documentID := strings.TrimSpace(in.DocumentID)
		parentBlockID := strings.TrimSpace(in.ParentBlockID)
		if documentID == "" || parentBlockID == "" {
			return "", fmt.Errorf("feishu_doc.list_children: document_id 和 parent_block_id 不能为空")
		}
		reqBuilder := larkdocx.NewGetDocumentBlockChildrenReqBuilder().
			DocumentId(documentID).
			BlockId(parentBlockID).
			PageSize(normalizePageSize(in.PageSize, 50, 500))
		if pageToken := strings.TrimSpace(in.PageToken); pageToken != "" {
			reqBuilder.PageToken(pageToken)
		}
		resp, err := client.Docx.DocumentBlockChildren.Get(ctx, reqBuilder.Build())
		if err != nil {
			return "", fmt.Errorf("feishu_doc.list_children 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_doc.list_children 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}

		items := []*larkdocx.Block{}
		pageToken := ""
		hasMore := false
		if resp.Data != nil {
			items = resp.Data.Items
			pageToken = strPtr(resp.Data.PageToken)
			hasMore = boolPtr(resp.Data.HasMore)
		}
		return mustJSON(map[string]any{
			"document_id":     documentID,
			"parent_block_id": parentBlockID,
			"items":           mapDocBlocks(items),
			"has_more":        hasMore,
			"page_token":      pageToken,
		}), nil

	case "append_text":
		documentID := strings.TrimSpace(in.DocumentID)
		parentBlockID := strings.TrimSpace(in.ParentBlockID)
		if documentID == "" || parentBlockID == "" {
			return "", fmt.Errorf("feishu_doc.append_text: document_id 和 parent_block_id 不能为空")
		}
		text := in.Text
		if strings.TrimSpace(text) == "" {
			return "", fmt.Errorf("feishu_doc.append_text: text 不能为空")
		}
		bodyBuilder := larkdocx.NewCreateDocumentBlockChildrenReqBodyBuilder().
			Children([]*larkdocx.Block{buildDocTextBlock(text)})
		if in.Index != nil {
			if *in.Index < 0 {
				return "", fmt.Errorf("feishu_doc.append_text: index 不能小于 0")
			}
			bodyBuilder.Index(*in.Index)
		}
		req := larkdocx.NewCreateDocumentBlockChildrenReqBuilder().
			DocumentId(documentID).
			BlockId(parentBlockID).
			Body(bodyBuilder.Build()).
			Build()
		resp, err := client.Docx.DocumentBlockChildren.Create(ctx, req)
		if err != nil {
			return "", fmt.Errorf("feishu_doc.append_text 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_doc.append_text 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}

		children := []*larkdocx.Block{}
		documentRevisionID := 0
		if resp.Data != nil {
			children = resp.Data.Children
			documentRevisionID = intPtr(resp.Data.DocumentRevisionId)
		}
		return mustJSON(map[string]any{
			"document_id":          documentID,
			"parent_block_id":      parentBlockID,
			"children":             mapDocBlocks(children),
			"document_revision_id": documentRevisionID,
		}), nil

	case "update_text":
		documentID := strings.TrimSpace(in.DocumentID)
		blockID := strings.TrimSpace(in.BlockID)
		if documentID == "" || blockID == "" {
			return "", fmt.Errorf("feishu_doc.update_text: document_id 和 block_id 不能为空")
		}
		text := in.Text
		if strings.TrimSpace(text) == "" {
			return "", fmt.Errorf("feishu_doc.update_text: text 不能为空")
		}

		updateReq := larkdocx.NewUpdateBlockRequestBuilder().
			BlockId(blockID).
			UpdateText(
				larkdocx.NewUpdateTextRequestBuilder().
					Elements(buildDocTextElements(text)).
					Build(),
			).
			Build()

		req := larkdocx.NewPatchDocumentBlockReqBuilder().
			DocumentId(documentID).
			BlockId(blockID).
			UpdateBlockRequest(updateReq).
			Build()
		resp, err := client.Docx.DocumentBlock.Patch(ctx, req)
		if err != nil {
			return "", fmt.Errorf("feishu_doc.update_text 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_doc.update_text 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}

		var block *larkdocx.Block
		documentRevisionID := 0
		if resp.Data != nil {
			block = resp.Data.Block
			documentRevisionID = intPtr(resp.Data.DocumentRevisionId)
		}
		return mustJSON(map[string]any{
			"block":                mapDocBlock(block),
			"document_revision_id": documentRevisionID,
		}), nil

	case "delete_children_range":
		documentID := strings.TrimSpace(in.DocumentID)
		parentBlockID := strings.TrimSpace(in.ParentBlockID)
		if documentID == "" || parentBlockID == "" {
			return "", fmt.Errorf("feishu_doc.delete_children_range: document_id 和 parent_block_id 不能为空")
		}
		if in.StartIndex == nil || in.EndIndex == nil {
			return "", fmt.Errorf("feishu_doc.delete_children_range: start_index 和 end_index 必填")
		}
		if err := validateDocChildrenRange(*in.StartIndex, *in.EndIndex); err != nil {
			return "", fmt.Errorf("feishu_doc.delete_children_range: %w", err)
		}

		req := larkdocx.NewBatchDeleteDocumentBlockChildrenReqBuilder().
			DocumentId(documentID).
			BlockId(parentBlockID).
			Body(
				larkdocx.NewBatchDeleteDocumentBlockChildrenReqBodyBuilder().
					StartIndex(*in.StartIndex).
					EndIndex(*in.EndIndex).
					Build(),
			).
			Build()
		resp, err := client.Docx.DocumentBlockChildren.BatchDelete(ctx, req)
		if err != nil {
			return "", fmt.Errorf("feishu_doc.delete_children_range 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_doc.delete_children_range 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}

		documentRevisionID := 0
		if resp.Data != nil {
			documentRevisionID = intPtr(resp.Data.DocumentRevisionId)
		}
		return mustJSON(map[string]any{
			"success":              true,
			"document_id":          documentID,
			"parent_block_id":      parentBlockID,
			"start_index":          *in.StartIndex,
			"end_index":            *in.EndIndex,
			"document_revision_id": documentRevisionID,
		}), nil

	default:
		return "", fmt.Errorf("feishu_doc: 未知 action=%q", in.Action)
	}
}

func validateDocChildrenRange(start, end int) error {
	if start < 0 || end < 0 {
		return fmt.Errorf("索引不能小于 0")
	}
	if end <= start {
		return fmt.Errorf("end_index 必须大于 start_index")
	}
	return nil
}

func buildDocTextBlock(text string) *larkdocx.Block {
	return larkdocx.NewBlockBuilder().
		BlockType(docxBlockTypeText).
		Text(
			larkdocx.NewTextBuilder().
				Elements(buildDocTextElements(text)).
				Build(),
		).
		Build()
}

func buildDocTextElements(text string) []*larkdocx.TextElement {
	return []*larkdocx.TextElement{
		larkdocx.NewTextElementBuilder().
			TextRun(larkdocx.NewTextRunBuilder().Content(text).Build()).
			Build(),
	}
}

func mapDocDocument(doc *larkdocx.Document) map[string]any {
	if doc == nil {
		return map[string]any{}
	}
	return map[string]any{
		"document_id": strPtr(doc.DocumentId),
		"revision_id": intPtr(doc.RevisionId),
		"title":       strPtr(doc.Title),
	}
}

func mapDocBlocks(blocks []*larkdocx.Block) []map[string]any {
	out := make([]map[string]any, 0, len(blocks))
	for _, b := range blocks {
		if b == nil {
			continue
		}
		out = append(out, mapDocBlock(b))
	}
	return out
}

func mapDocBlock(block *larkdocx.Block) map[string]any {
	if block == nil {
		return map[string]any{}
	}
	return map[string]any{
		"block_id":      strPtr(block.BlockId),
		"parent_id":     strPtr(block.ParentId),
		"children":      block.Children,
		"children_size": len(block.Children),
		"block_type":    intPtr(block.BlockType),
		"text":          extractDocBlockText(block),
	}
}

func extractDocBlockText(block *larkdocx.Block) string {
	if block == nil {
		return ""
	}
	texts := []*larkdocx.Text{
		block.Text,
		block.Heading1,
		block.Heading2,
		block.Heading3,
		block.Heading4,
		block.Heading5,
		block.Heading6,
		block.Heading7,
		block.Heading8,
		block.Heading9,
		block.Bullet,
		block.Ordered,
		block.Code,
		block.Quote,
		block.Equation,
		block.Todo,
	}
	for _, t := range texts {
		if t == nil {
			continue
		}
		if s := textElementToPlain(t); s != "" {
			return s
		}
	}
	return ""
}

func textElementToPlain(text *larkdocx.Text) string {
	if text == nil {
		return ""
	}
	var sb strings.Builder
	for _, e := range text.Elements {
		if e == nil {
			continue
		}
		switch {
		case e.TextRun != nil:
			sb.WriteString(strPtr(e.TextRun.Content))
		case e.MentionUser != nil:
			sb.WriteString("@user")
		case e.MentionDoc != nil:
			sb.WriteString("[doc]")
		case e.Equation != nil:
			sb.WriteString("[equation]")
		case e.File != nil:
			sb.WriteString("[file]")
		case e.LinkPreview != nil:
			sb.WriteString("[link]")
		}
	}
	return strings.TrimSpace(sb.String())
}

// ---------------------
// feishu_bitable
// ---------------------

type FeishuBitableTool struct {
	Cfg *config.Config
}

func (t *FeishuBitableTool) Name() string { return "feishu_bitable" }

func (t *FeishuBitableTool) Description() string {
	return `飞书多维表格工具。
支持 action：
- get_meta: 获取多维表格元信息
- create_app: 创建多维表格
- list_tables: 列出数据表
- list_fields: 列出字段
- create_field: 创建字段
- list_records: 列出记录
- get_record: 获取单条记录
- create_record: 创建记录
- update_record: 更新记录`
}

func (t *FeishuBitableTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["get_meta", "create_app", "list_tables", "list_fields", "create_field", "list_records", "get_record", "create_record", "update_record"],
				"description": "操作类型"
			},
			"app_token": {"type": "string", "description": "多维表格 app token"},
			"table_id": {"type": "string", "description": "数据表 ID"},
			"record_id": {"type": "string", "description": "记录 ID"},
			"name": {"type": "string", "description": "多维表格名称（create_app）"},
			"folder_token": {"type": "string", "description": "目录 token（create_app 可选）"},
			"time_zone": {"type": "string", "description": "时区（create_app 可选）"},
			"page_size": {"type": "integer", "description": "分页大小（list_*）"},
			"page_token": {"type": "string", "description": "分页 token（list_*）"},
			"view_id": {"type": "string", "description": "视图 ID（list_fields/list_records 可选）"},
			"filter": {"type": "string", "description": "过滤表达式（list_records 可选）"},
			"sort": {"type": "string", "description": "排序表达式 JSON 字符串（list_records 可选）"},
			"field_names": {
				"type": "array",
				"items": {"type": "string"},
				"description": "返回字段名列表（list_records 可选）"
			},
			"text_field_as_array": {"type": "boolean", "description": "是否按数组格式返回多行文本"},
			"user_id_type": {"type": "string", "description": "用户 ID 类型"},
			"field_name": {"type": "string", "description": "字段名（create_field）"},
			"field_type": {"type": "integer", "description": "字段类型（create_field）"},
			"fields": {"type": "object", "description": "记录字段 map（create_record/update_record）"},
			"ignore_consistency_check": {"type": "boolean", "description": "是否忽略一致性检查（create_record/update_record）"}
		},
		"required": ["action"]
	}`)
}

func (t *FeishuBitableTool) Execute(input json.RawMessage, _ *ExecContext) (string, error) {
	var in struct {
		Action                 string                 `json:"action"`
		AppToken               string                 `json:"app_token"`
		TableID                string                 `json:"table_id"`
		RecordID               string                 `json:"record_id"`
		Name                   string                 `json:"name"`
		FolderToken            string                 `json:"folder_token"`
		TimeZone               string                 `json:"time_zone"`
		PageSize               int                    `json:"page_size"`
		PageToken              string                 `json:"page_token"`
		ViewID                 string                 `json:"view_id"`
		Filter                 string                 `json:"filter"`
		Sort                   string                 `json:"sort"`
		FieldNames             []string               `json:"field_names"`
		TextFieldAsArray       *bool                  `json:"text_field_as_array"`
		UserIDType             string                 `json:"user_id_type"`
		FieldName              string                 `json:"field_name"`
		FieldType              int                    `json:"field_type"`
		Fields                 map[string]interface{} `json:"fields"`
		IgnoreConsistencyCheck *bool                  `json:"ignore_consistency_check"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("feishu_bitable 参数解析失败: %w", err)
	}
	if strings.TrimSpace(in.Action) == "" {
		return "", fmt.Errorf("feishu_bitable: action 不能为空")
	}

	base := feishuToolBase{Cfg: t.Cfg}
	client, err := base.newClient()
	if err != nil {
		return "", err
	}

	ctx, cancel := feishuCallCtx()
	defer cancel()

	switch in.Action {
	case "get_meta":
		appToken := strings.TrimSpace(in.AppToken)
		if appToken == "" {
			return "", fmt.Errorf("feishu_bitable.get_meta: app_token 不能为空")
		}
		resp, err := client.Bitable.App.Get(ctx, larkbitable.NewGetAppReqBuilder().AppToken(appToken).Build())
		if err != nil {
			return "", fmt.Errorf("feishu_bitable.get_meta 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_bitable.get_meta 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}

		var app *larkbitable.DisplayApp
		if resp.Data != nil {
			app = resp.Data.App
		}
		return mustJSON(map[string]any{
			"app": mapBitableDisplayApp(app),
		}), nil

	case "create_app":
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return "", fmt.Errorf("feishu_bitable.create_app: name 不能为空")
		}
		reqAppBuilder := larkbitable.NewReqAppBuilder().Name(name)
		if folderToken := strings.TrimSpace(in.FolderToken); folderToken != "" {
			reqAppBuilder.FolderToken(folderToken)
		}
		if timeZone := strings.TrimSpace(in.TimeZone); timeZone != "" {
			reqAppBuilder.TimeZone(timeZone)
		}
		resp, err := client.Bitable.App.Create(
			ctx,
			larkbitable.NewCreateAppReqBuilder().
				ReqApp(reqAppBuilder.Build()).
				Build(),
		)
		if err != nil {
			return "", fmt.Errorf("feishu_bitable.create_app 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_bitable.create_app 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}

		var app *larkbitable.App
		if resp.Data != nil {
			app = resp.Data.App
		}
		return mustJSON(map[string]any{
			"app": mapBitableApp(app),
		}), nil

	case "list_tables":
		appToken := strings.TrimSpace(in.AppToken)
		if appToken == "" {
			return "", fmt.Errorf("feishu_bitable.list_tables: app_token 不能为空")
		}
		reqBuilder := larkbitable.NewListAppTableReqBuilder().
			AppToken(appToken).
			PageSize(normalizePageSize(in.PageSize, 50, 200))
		if pageToken := strings.TrimSpace(in.PageToken); pageToken != "" {
			reqBuilder.PageToken(pageToken)
		}
		resp, err := client.Bitable.AppTable.List(ctx, reqBuilder.Build())
		if err != nil {
			return "", fmt.Errorf("feishu_bitable.list_tables 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_bitable.list_tables 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}

		items := []*larkbitable.AppTable{}
		hasMore := false
		pageToken := ""
		total := 0
		if resp.Data != nil {
			items = resp.Data.Items
			hasMore = boolPtr(resp.Data.HasMore)
			pageToken = strPtr(resp.Data.PageToken)
			total = intPtr(resp.Data.Total)
		}
		return mustJSON(map[string]any{
			"app_token":  appToken,
			"tables":     mapBitableTables(items),
			"has_more":   hasMore,
			"page_token": pageToken,
			"total":      total,
		}), nil

	case "list_fields":
		appToken := strings.TrimSpace(in.AppToken)
		tableID := strings.TrimSpace(in.TableID)
		if appToken == "" || tableID == "" {
			return "", fmt.Errorf("feishu_bitable.list_fields: app_token 和 table_id 不能为空")
		}
		reqBuilder := larkbitable.NewListAppTableFieldReqBuilder().
			AppToken(appToken).
			TableId(tableID).
			PageSize(normalizePageSize(in.PageSize, 50, 200))
		if pageToken := strings.TrimSpace(in.PageToken); pageToken != "" {
			reqBuilder.PageToken(pageToken)
		}
		if viewID := strings.TrimSpace(in.ViewID); viewID != "" {
			reqBuilder.ViewId(viewID)
		}
		if in.TextFieldAsArray != nil {
			reqBuilder.TextFieldAsArray(*in.TextFieldAsArray)
		}
		resp, err := client.Bitable.AppTableField.List(ctx, reqBuilder.Build())
		if err != nil {
			return "", fmt.Errorf("feishu_bitable.list_fields 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_bitable.list_fields 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}

		items := []*larkbitable.AppTableFieldForList{}
		hasMore := false
		pageToken := ""
		total := 0
		if resp.Data != nil {
			items = resp.Data.Items
			hasMore = boolPtr(resp.Data.HasMore)
			pageToken = strPtr(resp.Data.PageToken)
			total = intPtr(resp.Data.Total)
		}
		return mustJSON(map[string]any{
			"app_token":  appToken,
			"table_id":   tableID,
			"fields":     mapBitableFields(items),
			"has_more":   hasMore,
			"page_token": pageToken,
			"total":      total,
		}), nil

	case "create_field":
		appToken := strings.TrimSpace(in.AppToken)
		tableID := strings.TrimSpace(in.TableID)
		fieldName := strings.TrimSpace(in.FieldName)
		if appToken == "" || tableID == "" || fieldName == "" {
			return "", fmt.Errorf("feishu_bitable.create_field: app_token、table_id、field_name 不能为空")
		}
		if err := validateBitableFieldType(in.FieldType); err != nil {
			return "", fmt.Errorf("feishu_bitable.create_field: %w", err)
		}
		field := larkbitable.NewAppTableFieldBuilder().
			FieldName(fieldName).
			Type(in.FieldType).
			Build()
		resp, err := client.Bitable.AppTableField.Create(
			ctx,
			larkbitable.NewCreateAppTableFieldReqBuilder().
				AppToken(appToken).
				TableId(tableID).
				AppTableField(field).
				Build(),
		)
		if err != nil {
			return "", fmt.Errorf("feishu_bitable.create_field 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_bitable.create_field 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}

		var fieldOut *larkbitable.AppTableField
		if resp.Data != nil {
			fieldOut = resp.Data.Field
		}
		return mustJSON(map[string]any{
			"field": mapBitableField(fieldOut),
		}), nil

	case "list_records":
		appToken := strings.TrimSpace(in.AppToken)
		tableID := strings.TrimSpace(in.TableID)
		if appToken == "" || tableID == "" {
			return "", fmt.Errorf("feishu_bitable.list_records: app_token 和 table_id 不能为空")
		}
		reqBuilder := larkbitable.NewListAppTableRecordReqBuilder().
			AppToken(appToken).
			TableId(tableID).
			PageSize(normalizePageSize(in.PageSize, 50, 500))
		if pageToken := strings.TrimSpace(in.PageToken); pageToken != "" {
			reqBuilder.PageToken(pageToken)
		}
		if viewID := strings.TrimSpace(in.ViewID); viewID != "" {
			reqBuilder.ViewId(viewID)
		}
		if filter := strings.TrimSpace(in.Filter); filter != "" {
			reqBuilder.Filter(filter)
		}
		if sort := strings.TrimSpace(in.Sort); sort != "" {
			reqBuilder.Sort(sort)
		}
		if len(in.FieldNames) > 0 {
			fieldNamesJSON, err := json.Marshal(in.FieldNames)
			if err != nil {
				return "", fmt.Errorf("feishu_bitable.list_records: field_names 编码失败: %w", err)
			}
			reqBuilder.FieldNames(string(fieldNamesJSON))
		}
		if in.TextFieldAsArray != nil {
			reqBuilder.TextFieldAsArray(*in.TextFieldAsArray)
		}
		if userIDType := strings.TrimSpace(in.UserIDType); userIDType != "" {
			reqBuilder.UserIdType(userIDType)
		}
		resp, err := client.Bitable.AppTableRecord.List(ctx, reqBuilder.Build())
		if err != nil {
			return "", fmt.Errorf("feishu_bitable.list_records 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_bitable.list_records 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}

		items := []*larkbitable.AppTableRecord{}
		hasMore := false
		pageToken := ""
		total := 0
		if resp.Data != nil {
			items = resp.Data.Items
			hasMore = boolPtr(resp.Data.HasMore)
			pageToken = strPtr(resp.Data.PageToken)
			total = intPtr(resp.Data.Total)
		}
		return mustJSON(map[string]any{
			"app_token":  appToken,
			"table_id":   tableID,
			"records":    mapBitableRecords(items),
			"has_more":   hasMore,
			"page_token": pageToken,
			"total":      total,
		}), nil

	case "get_record":
		appToken := strings.TrimSpace(in.AppToken)
		tableID := strings.TrimSpace(in.TableID)
		recordID := strings.TrimSpace(in.RecordID)
		if appToken == "" || tableID == "" || recordID == "" {
			return "", fmt.Errorf("feishu_bitable.get_record: app_token、table_id、record_id 不能为空")
		}
		reqBuilder := larkbitable.NewGetAppTableRecordReqBuilder().
			AppToken(appToken).
			TableId(tableID).
			RecordId(recordID)
		if in.TextFieldAsArray != nil {
			reqBuilder.TextFieldAsArray(*in.TextFieldAsArray)
		}
		if userIDType := strings.TrimSpace(in.UserIDType); userIDType != "" {
			reqBuilder.UserIdType(userIDType)
		}
		resp, err := client.Bitable.AppTableRecord.Get(ctx, reqBuilder.Build())
		if err != nil {
			return "", fmt.Errorf("feishu_bitable.get_record 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_bitable.get_record 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}

		var record *larkbitable.AppTableRecord
		if resp.Data != nil {
			record = resp.Data.Record
		}
		return mustJSON(map[string]any{
			"record": mapBitableRecord(record),
		}), nil

	case "create_record":
		appToken := strings.TrimSpace(in.AppToken)
		tableID := strings.TrimSpace(in.TableID)
		if appToken == "" || tableID == "" {
			return "", fmt.Errorf("feishu_bitable.create_record: app_token 和 table_id 不能为空")
		}
		if in.Fields == nil {
			return "", fmt.Errorf("feishu_bitable.create_record: fields 不能为空")
		}
		record := larkbitable.NewAppTableRecordBuilder().Fields(in.Fields).Build()
		reqBuilder := larkbitable.NewCreateAppTableRecordReqBuilder().
			AppToken(appToken).
			TableId(tableID).
			AppTableRecord(record)
		if userIDType := strings.TrimSpace(in.UserIDType); userIDType != "" {
			reqBuilder.UserIdType(userIDType)
		}
		if in.IgnoreConsistencyCheck != nil {
			reqBuilder.IgnoreConsistencyCheck(*in.IgnoreConsistencyCheck)
		}
		resp, err := client.Bitable.AppTableRecord.Create(ctx, reqBuilder.Build())
		if err != nil {
			return "", fmt.Errorf("feishu_bitable.create_record 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_bitable.create_record 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}

		var recordOut *larkbitable.AppTableRecord
		if resp.Data != nil {
			recordOut = resp.Data.Record
		}
		return mustJSON(map[string]any{
			"record": mapBitableRecord(recordOut),
		}), nil

	case "update_record":
		appToken := strings.TrimSpace(in.AppToken)
		tableID := strings.TrimSpace(in.TableID)
		recordID := strings.TrimSpace(in.RecordID)
		if appToken == "" || tableID == "" || recordID == "" {
			return "", fmt.Errorf("feishu_bitable.update_record: app_token、table_id、record_id 不能为空")
		}
		if in.Fields == nil {
			return "", fmt.Errorf("feishu_bitable.update_record: fields 不能为空")
		}
		record := larkbitable.NewAppTableRecordBuilder().Fields(in.Fields).Build()
		reqBuilder := larkbitable.NewUpdateAppTableRecordReqBuilder().
			AppToken(appToken).
			TableId(tableID).
			RecordId(recordID).
			AppTableRecord(record)
		if userIDType := strings.TrimSpace(in.UserIDType); userIDType != "" {
			reqBuilder.UserIdType(userIDType)
		}
		if in.IgnoreConsistencyCheck != nil {
			reqBuilder.IgnoreConsistencyCheck(*in.IgnoreConsistencyCheck)
		}
		resp, err := client.Bitable.AppTableRecord.Update(ctx, reqBuilder.Build())
		if err != nil {
			return "", fmt.Errorf("feishu_bitable.update_record 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_bitable.update_record 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}

		var recordOut *larkbitable.AppTableRecord
		if resp.Data != nil {
			recordOut = resp.Data.Record
		}
		return mustJSON(map[string]any{
			"record": mapBitableRecord(recordOut),
		}), nil

	default:
		return "", fmt.Errorf("feishu_bitable: 未知 action=%q", in.Action)
	}
}

func validateBitableFieldType(fieldType int) error {
	if fieldType <= 0 {
		return fmt.Errorf("field_type 必须为正整数")
	}
	return nil
}

func mapBitableDisplayApp(app *larkbitable.DisplayApp) map[string]any {
	if app == nil {
		return map[string]any{}
	}
	return map[string]any{
		"app_token":       strPtr(app.AppToken),
		"name":            strPtr(app.Name),
		"revision":        intPtr(app.Revision),
		"is_advanced":     boolPtr(app.IsAdvanced),
		"time_zone":       strPtr(app.TimeZone),
		"formula_type":    intPtr(app.FormulaType),
		"advance_version": strPtr(app.AdvanceVersion),
	}
}

func mapBitableApp(app *larkbitable.App) map[string]any {
	if app == nil {
		return map[string]any{}
	}
	return map[string]any{
		"app_token":        strPtr(app.AppToken),
		"name":             strPtr(app.Name),
		"revision":         intPtr(app.Revision),
		"folder_token":     strPtr(app.FolderToken),
		"url":              strPtr(app.Url),
		"default_table_id": strPtr(app.DefaultTableId),
		"time_zone":        strPtr(app.TimeZone),
	}
}

func mapBitableTables(items []*larkbitable.AppTable) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		if it == nil {
			continue
		}
		out = append(out, map[string]any{
			"table_id": strPtr(it.TableId),
			"name":     strPtr(it.Name),
			"revision": intPtr(it.Revision),
		})
	}
	return out
}

func mapBitableFields(items []*larkbitable.AppTableFieldForList) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		if it == nil {
			continue
		}
		out = append(out, map[string]any{
			"field_id":   strPtr(it.FieldId),
			"field_name": strPtr(it.FieldName),
			"type":       intPtr(it.Type),
			"ui_type":    strPtr(it.UiType),
			"is_primary": boolPtr(it.IsPrimary),
			"is_hidden":  boolPtr(it.IsHidden),
		})
	}
	return out
}

func mapBitableField(field *larkbitable.AppTableField) map[string]any {
	if field == nil {
		return map[string]any{}
	}
	return map[string]any{
		"field_id":   strPtr(field.FieldId),
		"field_name": strPtr(field.FieldName),
		"type":       intPtr(field.Type),
		"ui_type":    strPtr(field.UiType),
		"is_primary": boolPtr(field.IsPrimary),
		"is_hidden":  boolPtr(field.IsHidden),
	}
}

func mapBitableRecords(items []*larkbitable.AppTableRecord) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		if it == nil {
			continue
		}
		out = append(out, mapBitableRecord(it))
	}
	return out
}

func mapBitableRecord(record *larkbitable.AppTableRecord) map[string]any {
	if record == nil {
		return map[string]any{}
	}
	return map[string]any{
		"record_id":          strPtr(record.RecordId),
		"fields":             record.Fields,
		"created_time":       int64Ptr(record.CreatedTime),
		"last_modified_time": int64Ptr(record.LastModifiedTime),
		"shared_url":         strPtr(record.SharedUrl),
		"record_url":         strPtr(record.RecordUrl),
	}
}

// ---------------------
// feishu_perm
// ---------------------

type FeishuPermTool struct {
	Cfg *config.Config
}

func (t *FeishuPermTool) Name() string { return "feishu_perm" }

func (t *FeishuPermTool) Description() string {
	return `飞书文档权限成员工具。
支持 action：
- list: 列出协作者
- add: 添加协作者权限
- remove: 移除协作者权限`
}

func (t *FeishuPermTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["list", "add", "remove"],
				"description": "操作类型"
			},
			"token": {"type": "string", "description": "文件 token"},
			"file_type": {
				"type": "string",
				"enum": ["doc", "docx", "sheet", "bitable", "file", "folder", "mindnote", "slides", "wiki"],
				"description": "文件类型"
			},
			"member_type": {
				"type": "string",
				"enum": ["openid", "open_id", "user_id", "union_id", "email"],
				"description": "协作者 ID 类型（add/remove）"
			},
			"member_id": {"type": "string", "description": "协作者 ID（add/remove）"},
			"perm": {"type": "string", "description": "权限角色（add，默认 view）"},
			"perm_type": {"type": "string", "description": "权限类型（默认 container）"},
			"collaborator_type": {"type": "string", "description": "协作者对象类型（默认 user）"},
			"need_notification": {"type": "boolean", "description": "是否通知对方（add）"},
			"fields": {"type": "string", "description": "list 返回字段，默认 *"}
		},
		"required": ["action"]
	}`)
}

func (t *FeishuPermTool) Execute(input json.RawMessage, _ *ExecContext) (string, error) {
	var in struct {
		Action           string `json:"action"`
		Token            string `json:"token"`
		FileType         string `json:"file_type"`
		MemberType       string `json:"member_type"`
		MemberID         string `json:"member_id"`
		Perm             string `json:"perm"`
		PermType         string `json:"perm_type"`
		CollaboratorType string `json:"collaborator_type"`
		NeedNotification *bool  `json:"need_notification"`
		Fields           string `json:"fields"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return "", fmt.Errorf("feishu_perm 参数解析失败: %w", err)
	}
	if strings.TrimSpace(in.Action) == "" {
		return "", fmt.Errorf("feishu_perm: action 不能为空")
	}

	base := feishuToolBase{Cfg: t.Cfg}
	client, err := base.newClient()
	if err != nil {
		return "", err
	}

	token := strings.TrimSpace(in.Token)
	if token == "" {
		return "", fmt.Errorf("feishu_perm: token 不能为空")
	}
	fileType, err := normalizePermissionFileType(in.FileType)
	if err != nil {
		return "", fmt.Errorf("feishu_perm: %w", err)
	}

	ctx, cancel := feishuCallCtx()
	defer cancel()

	switch in.Action {
	case "list":
		reqBuilder := larkdrive.NewListPermissionMemberReqBuilder().
			Token(token).
			Type(fileType)
		fields := strings.TrimSpace(in.Fields)
		if fields == "" {
			fields = "*"
		}
		reqBuilder.Fields(fields)
		if permType := strings.TrimSpace(in.PermType); permType != "" {
			reqBuilder.PermType(permType)
		}
		resp, err := client.Drive.PermissionMember.List(ctx, reqBuilder.Build())
		if err != nil {
			return "", fmt.Errorf("feishu_perm.list 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_perm.list 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}

		items := []*larkdrive.Member{}
		if resp.Data != nil {
			items = resp.Data.Items
		}
		return mustJSON(map[string]any{
			"token":     token,
			"file_type": fileType,
			"members":   mapPermissionMembers(items),
		}), nil

	case "add":
		memberID := strings.TrimSpace(in.MemberID)
		if memberID == "" {
			return "", fmt.Errorf("feishu_perm.add: member_id 不能为空")
		}
		memberType, err := normalizePermissionMemberType(in.MemberType)
		if err != nil {
			return "", fmt.Errorf("feishu_perm.add: %w", err)
		}
		needNotification := false
		if in.NeedNotification != nil {
			needNotification = *in.NeedNotification
		}
		perm := normalizePermissionPerm(in.Perm)
		permType := normalizePermissionPermType(in.PermType)
		collaboratorType := normalizePermissionCollaboratorType(in.CollaboratorType)

		baseMember := larkdrive.NewBaseMemberBuilder().
			MemberType(memberType).
			MemberId(memberID).
			Perm(perm).
			PermType(permType).
			Type(collaboratorType).
			Build()

		req := larkdrive.NewCreatePermissionMemberReqBuilder().
			Token(token).
			Type(fileType).
			NeedNotification(needNotification).
			BaseMember(baseMember).
			Build()

		resp, err := client.Drive.PermissionMember.Create(ctx, req)
		if err != nil {
			return "", fmt.Errorf("feishu_perm.add 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_perm.add 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}

		var member *larkdrive.BaseMember
		if resp.Data != nil {
			member = resp.Data.Member
		}
		return mustJSON(map[string]any{
			"success": true,
			"member":  mapPermissionBaseMember(member),
		}), nil

	case "remove":
		memberID := strings.TrimSpace(in.MemberID)
		if memberID == "" {
			return "", fmt.Errorf("feishu_perm.remove: member_id 不能为空")
		}
		memberType, err := normalizePermissionMemberType(in.MemberType)
		if err != nil {
			return "", fmt.Errorf("feishu_perm.remove: %w", err)
		}
		permType := normalizePermissionPermType(in.PermType)
		collaboratorType := normalizePermissionCollaboratorType(in.CollaboratorType)

		req := larkdrive.NewDeletePermissionMemberReqBuilder().
			Token(token).
			MemberId(memberID).
			Type(fileType).
			MemberType(memberType).
			Body(
				larkdrive.NewDeletePermissionMemberReqBodyBuilder().
					Type(collaboratorType).
					PermType(permType).
					Build(),
			).
			Build()
		resp, err := client.Drive.PermissionMember.Delete(ctx, req)
		if err != nil {
			return "", fmt.Errorf("feishu_perm.remove 调用失败: %w", err)
		}
		if !resp.Success() {
			return "", fmt.Errorf("feishu_perm.remove 失败: code=%d msg=%s", resp.Code, resp.Msg)
		}

		return mustJSON(map[string]any{
			"success":     true,
			"token":       token,
			"file_type":   fileType,
			"member_type": memberType,
			"member_id":   memberID,
		}), nil

	default:
		return "", fmt.Errorf("feishu_perm: 未知 action=%q", in.Action)
	}
}

func normalizePermissionFileType(v string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return "", fmt.Errorf("file_type 不能为空")
	}
	switch v {
	case "doc", "docx", "sheet", "bitable", "file", "folder", "mindnote", "slides", "wiki":
		return v, nil
	default:
		return "", fmt.Errorf("file_type 非法: %q", v)
	}
}

func normalizePermissionMemberType(v string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "openid", "open_id":
		return "openid", nil
	case "user_id", "userid":
		return "user_id", nil
	case "union_id", "unionid":
		return "union_id", nil
	case "email":
		return "email", nil
	default:
		return "", fmt.Errorf("member_type 非法: %q", v)
	}
}

func normalizePermissionPerm(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "", "read", "view":
		return "view"
	case "write", "edit":
		return "edit"
	case "owner", "fullaccess", "full_access":
		return "full_access"
	default:
		return v
	}
}

func normalizePermissionPermType(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return "container"
	}
	return v
}

func normalizePermissionCollaboratorType(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return "user"
	}
	return v
}

func mapPermissionMembers(items []*larkdrive.Member) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		if it == nil {
			continue
		}
		out = append(out, map[string]any{
			"member_type":    strPtr(it.MemberType),
			"member_id":      strPtr(it.MemberId),
			"perm":           strPtr(it.Perm),
			"perm_type":      strPtr(it.PermType),
			"type":           strPtr(it.Type),
			"name":           strPtr(it.Name),
			"avatar":         strPtr(it.Avatar),
			"external_label": boolPtr(it.ExternalLabel),
		})
	}
	return out
}

func mapPermissionBaseMember(member *larkdrive.BaseMember) map[string]any {
	if member == nil {
		return map[string]any{}
	}
	return map[string]any{
		"member_type": strPtr(member.MemberType),
		"member_id":   strPtr(member.MemberId),
		"perm":        strPtr(member.Perm),
		"perm_type":   strPtr(member.PermType),
		"type":        strPtr(member.Type),
	}
}

func int64Ptr(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}
