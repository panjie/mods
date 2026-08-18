# 任务计划工具（todo_write）— 实施计划

**日期：** 2026-08-18
**规格：** `docs/superpowers/specs/2026-08-18-todo-plan-tool-design.md`
**方式：** 每个任务 TDD（先写失败测试再实现）；完成后 `go run github.com/go-task/task/v3/cmd/task@v3.51.1 check && go run github.com/go-task/task/v3/cmd/task@v3.51.1 test` 全绿。

## Task 1 — 工具：internal/tools/todo.go

- 测试 `todo_test.go`：注册成功、`Capabilities.ReadOnly=true`、schema required 含 `todos`、三态输出格式、校验错误（空清单 / >20 项 / 非法 status / 空 content）。
- 实现 `RegisterTodoWrite(registry)` + `TodoWriteToolName` 常量；Call 返回 `Plan (N items) — X completed, Y in progress` + 编号清单行。
- 验证：`go test ./internal/tools -run TestTodoWrite -count=1`。

## Task 2 — 面板：internal/ui/todo_panel.go

- 测试 `todo_panel_test.go`：`RenderTodoPanel` 含 `PLAN` 标题、三态行 `[x]/[~]/[ ]`、Meta 进度文本；`TodoItemsFromArgs` 解析/容错；`TodoSummary`（"3 items · 1 completed"）；`ToolOperationLabel("todo_write")` = "Updating plan"。
- 实现 `TodoItem`、`TodoItemsFromArgs`、`RenderTodoPanel`（复用 `InteractionPanel`）、`TodoSummary`。
- 验证：`go test ./internal/ui -run TestTodo -count=1`。

## Task 3 — app 特判：internal/app/todo_panel.go + tool_result 摘要

- 测试（app 包）：TTY 下 `toolResultOutputCmd("todo_write",…)` 注入 display block（`m.Output` 含纯文本清单、`displayBlocks` 含 PLAN 面板）且无 stderr 一行记录；非 TTY 下 stderr 输出 `✓ todo_write: N items · M completed`；raw/minimal/hide-tool-status 全部抑制。
- 实现：`toolResultOutputCmd` 特判分支 + `ui/tool_result.go` 的 `toolResultSummary` 加 `todo_write` case。
- 验证：`go test ./internal/app -run TestTodoWrite -count=1`。

## Task 4 — 行为策略：internal/prompts/identity.md

- 测试：`TestIdentityHasPlanningPolicy`（含 `todo_write`、≥3 步、恰好一项 in_progress、全量重发）；体积预算测试回归通过。
- 实现：新增 "Planning multi-step work" 段。
- 验证：`go test ./internal/prompts -count=1`。

## Task 5 — 注册：internal/tooling/tools.go

- 测试：`BuildRegistry` 含 `todo_write`；`BuiltinSpecs` 收录且 ReadOnly=true。
- 实现：`BuildRegistry` 在 `RegisterModsHelp` 后无条件注册；`buildBuiltinSpecs` 同步收录。
- 验证：`go test ./internal/tooling -count=1`。

## Task 6 — 收尾验证

- `task check`（go build ./...）+ `task test`（根模块 + internal/huh）全绿。
- `golangci-lint run ./...`（如已安装）无新增告警。
- 手动冒烟（可选）：TUI 中给一个多步任务，确认 PLAN 面板出现且状态随执行更新。

## Self-Review

- 无 DB 迁移、无新配置项、无 InteractionHandlers 变更 — 符合非目标。
- 全部新文件 LF 行尾。
- 工具错误信息带条目序号，便于模型自纠。
