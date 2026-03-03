# Feishu Tools Usage & Scopes

Last updated: 2026-03-03

## Overview

Runtime registers these Feishu tools when `FEISHU_APP_ID` and `FEISHU_APP_SECRET` are configured:

- `feishu_chat`
- `feishu_wiki`
- `feishu_drive`
- `feishu_doc`
- `feishu_bitable`
- `feishu_perm`

## Suggested App Scopes

Use this as a minimal checklist in Feishu Open Platform, then refine by least privilege:

- `feishu_chat`
  - IM chat info read
  - IM chat member read
- `feishu_wiki`
  - Wiki space read/write
  - Wiki node read/write
- `feishu_drive`
  - Drive file read/write
  - Drive folder create/move/delete
- `feishu_doc`
  - Docx read
  - Docx block write/update/delete
- `feishu_bitable`
  - Bitable app/table/field/record read/write
- `feishu_perm`
  - Drive permission member read/write

## Quick Examples

### `feishu_chat`

```json
{"action":"info","chat_id":"oc_xxx"}
```

### `feishu_wiki`

```json
{"action":"spaces"}
```

### `feishu_drive`

```json
{"action":"list","folder_token":"0","page_size":50}
```

### `feishu_doc`

```json
{"action":"read","document_id":"doxcxxx"}
```

### `feishu_bitable`

```json
{"action":"list_records","app_token":"appxxx","table_id":"tblxxx","page_size":100}
```

### `feishu_perm`

```json
{"action":"list","token":"docxxx","file_type":"doc"}
```

## Retry Policy

All Feishu tool HTTP calls use a shared retry client:

- max attempts: `3`
- retry status codes: `429`, `500`, `502`, `503`, `504`
- retry error classes: network timeout/reset/temporary EOF-class errors
- backoff: exponential (`200ms`, `400ms`, `800ms`)
