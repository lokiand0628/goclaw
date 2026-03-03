# Feishu Agent Support Progress

Last updated: 2026-03-03

## Context

Goal: bring `goclaw` Feishu support closer to OpenClaw's Feishu plugin capability, while keeping implementation in Go.

## Progress (Done)

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

Related files:

- `internal/tools/feishu_tools.go`
- `internal/tools/feishu_tools_phase3.go`
- `internal/tools/feishu_tools_test.go`

## Verification

- Passed:
  - `go test ./internal/tools ./internal/agent`
- Known existing repo-level issue (not introduced by this phase):
  - `go test ./...` fails at `internal/assets/assets.go` because `//go:embed workspace/*` has no matching files in current checkout.

## Plan

### Phase 2 - Wire-up and runtime integration

1. Integrate `RegisterFeishuTools(...)` into actual runtime/tool bootstrap path.
2. Ensure all agents that should use Feishu tools can see these tool definitions.
3. Add startup logs for Feishu tool registration success/failure.

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

- [ ] Find and patch the actual tool bootstrap code path (`cmd/clawdbot` or equivalent runtime entry) to call `RegisterFeishuTools`.
- [ ] Add integration tests (or smoke tests) for each Feishu tool action with mocked SDK responses.
- [ ] Introduce a lightweight Feishu API client abstraction for easier mocking and retry/error policy consistency.
- [ ] Add user-facing docs for tool usage examples and required Feishu scopes.
- [ ] Add retries / rate-limit handling / transient error policy for Feishu API calls.

## Notes for next session

- Core Feishu tool surface is now implemented in tools layer; runtime exposure still depends on bootstrap wiring.
- Keep tool action names stable to avoid prompt/tool-schema churn.
