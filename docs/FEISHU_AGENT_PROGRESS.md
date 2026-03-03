# Feishu Agent Support Progress

Last updated: 2026-03-03

## Context

Goal: bring `goclaw` Feishu support closer to OpenClaw's Feishu plugin capability, while keeping implementation in Go.

## Progress (Done)

### Phase 2 - Wire-up and runtime integration (completed)

- Added runtime bootstrap entry:
  - `cmd/clawdbot/main.go`
  - commands: `start`, `sessions list`, `rollback [tag]`, `rollback list`
- Wired Feishu tools into real startup path:
  - runtime now calls `RegisterFeishuTools(...)` during tool registry initialization
  - startup logs explicitly report Feishu tool registration status
- Integrated runtime toolset and scheduler bindings:
  - core tools + Feishu tools are visible to all configured agents
  - `manage_cron` now binds to `scheduler.CronScheduler` at runtime
- Added minimal embedded workspace template file so `internal/assets` embed can compile in current checkout.

### Phase 1 - Core tools scaffold (completed)

- Added new Feishu agent tools in Go:
  - `feishu_chat`
    - actions: `info`, `members`
  - `feishu_wiki`
    - actions: `spaces`, `nodes`, `get`, `create`, `move`, `rename`
  - `feishu_drive`
    - actions: `list`, `info`, `create_folder`, `move`, `delete`
- Added shared registration entry:
  - `RegisterFeishuTools(reg *Registry, cfg *config.Config) error`
- Added unit tests:
  - tool registration behavior
  - wiki/drive type normalization behavior

### Phase 3 - Feature coverage expansion (completed for core actions)

- Added new Feishu tools in Go:
  - `feishu_doc`
    - actions: `create`, `read`, `get_block`, `list_children`, `append_text`, `update_text`, `delete_children_range`
  - `feishu_bitable`
    - actions: `get_meta`, `create_app`, `list_tables`, `list_fields`, `create_field`, `list_records`, `get_record`, `create_record`, `update_record`
  - `feishu_perm`
    - actions: `list`, `add`, `remove`
- Extended shared registration:
  - `RegisterFeishuTools(...)` now registers all 6 Feishu tools:
    - `feishu_chat`, `feishu_wiki`, `feishu_drive`, `feishu_doc`, `feishu_bitable`, `feishu_perm`
- Added unit tests for:
  - registration coverage of all Feishu tools
  - doc range validation
  - bitable field-type validation
  - permission file/member type normalization

### Phase 3.5 - Reliability and testability hardening (completed)

- Added shared Feishu HTTP retry policy in tool client path:
  - retries on `429/500/502/503/504`
  - retries on transient network errors (timeout/reset/EOF class)
  - exponential backoff (`200ms`, `400ms`, `800ms`)
- Added mock-server smoke tests that execute one real action for each Feishu tool:
  - `feishu_chat`, `feishu_wiki`, `feishu_drive`, `feishu_doc`, `feishu_bitable`, `feishu_perm`
- Extended test coverage to action-level execution for all current Feishu tool actions (32 actions in total) with mocked OpenAPI responses.
- Added retry behavior test (first request `502`, second success) to verify retry path.
- Added user-facing usage/scope doc:
  - `docs/FEISHU_TOOL_USAGE.md`
- Added optional Feishu base URL override:
  - config field `channels.feishu.openBaseURL`
  - env `FEISHU_OPEN_BASE_URL`

Related files:

- `cmd/clawdbot/main.go`
- `internal/agent/loop.go`
- `internal/assets/workspace/README.md`
- `docs/FEISHU_TOOL_USAGE.md`
- `internal/tools/feishu_tools.go`
- `internal/tools/feishu_tools_phase3.go`
- `internal/tools/feishu_tools_test.go`

## Verification

- Passed:
  - `go test ./...`
  - `go build -o /tmp/goclaw-build ./cmd/clawdbot/`

## Plan

### Phase 3 - Deepen tool behaviors

1. Add richer `feishu_doc` actions (batch update block styles, descendant ops, table/image block ops).
2. Add richer `feishu_bitable` actions (batch CRUD, field update/delete, view-level operations).
3. Add richer `feishu_perm` actions (batch grant, update permission role, owner transfer).

### Phase 4 - Channel capability alignment

1. Multi-account config model.
2. WebSocket/Webhook dual connection mode support.
3. Group policy / mention policy / thread reply strategy alignment.
4. Media/card sending and richer outbound formatting.

## TODO

- [ ] Introduce per-resource typed Feishu API interfaces (chat/wiki/drive/docx/bitable/perm) for deeper unit-level mocking and finer-grained failure simulation.

## Notes for next session

- Feishu tools are now wired into runtime bootstrap and exposed in agent tool registry.
- Keep tool action names stable to avoid prompt/tool-schema churn.
