# Feishu Agent Support Progress

Last updated: 2026-03-02

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

Related files:

- `internal/tools/feishu_tools.go`
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

### Phase 3 - Expand feature coverage

1. Add `feishu_doc` (docx read/write/update block operations).
2. Add `feishu_bitable` (metadata/field/record CRUD).
3. Add `feishu_perm` (permission member list/add/remove).

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
- [ ] Start Phase 3 (`feishu_doc`, `feishu_bitable`, `feishu_perm`).

## Notes for next session

- Current code is ready for registration and usage once bootstrap wiring exists.
- Keep tool action names stable to avoid prompt/tool-schema churn.
