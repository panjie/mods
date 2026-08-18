# 任务计划工具（todo_write）— 设计

**日期：** 2026-08-18
**状态：** Approved（用户已批准：仅展示模式 + 内联面板）
**范围：** `internal/tools`（新工具）、`internal/ui`（面板渲染）、`internal/app`（结果特判）、`internal/prompts`（行为策略）、`internal/tooling`（注册）

## 1 背景与问题

复杂任务（如"加速 Emacs 启动"）中，模型会连续执行大量探索性命令，用户只能看到一串 `✓/✗` 工具记录，无法把握整体进度与当前所处阶段。需要一个 opencode todowrite 式的机制：模型先形成分步计划，随执行更新状态，用户在 TUI 中实时可见。

## 2 目标矩阵

| 目标 | 决策 |
|---|---|
| 交互模式 | 仅展示（不阻塞执行，无确认门） |
| 展示位置 | 内联清单面板（复用 display-block 机制，同 Thinking 面板） |
| 触发条件 | 模型自行判断：约 ≥3 步或多阶段工具调用才写计划（identity.md 策略约束） |
| 持久化 | 免费复用 `session_messages`（工具调用/结果天然随会话存取，`--continue` 可见） |
| 配置 | 不加开关，无条件注册（同 `mods_help`，无副作用） |
| 审批 | 只读无目录 → 自动放行，不弹审批横幅 |

## 3 关键决策

1. **全量替换语义**：每次调用传完整清单，无服务端隐藏状态；幂等、可重放，并行调用语义安全。
2. **状态即消息**：工具结果字符串就是格式化清单（模型在 RoleTool 消息中看到最新状态），UI 不向工具回流。
3. **UI 特判而非交互通道**：app 层在 `toolResultOutputCmd` 按工具名特判（parse Arguments → display block），不新增 `InteractionHandlers` 条目。
4. **TTY 渲染面板、非 TTY 走一行 stderr 摘要**：保持 stdout"仅模型输出"的契约（`toolResultOutputCmd` 既有约定）。

## 4 架构

```
模型 ──todo_write(todos)──► Registry.Call（校验+格式化，返回清单文本）
   │                              │
   │                              └─ RoleTool 消息（随 session_messages 持久化）
   ▼
handleToolCallsDone → toolResultOutputCmd
   ├─ TTY：ui.RenderTodoPanel → appendToOutputWithDisplayBlock（内联 PLAN 面板）
   └─ 非 TTY：ToolResultStatus 一行摘要 → stderr
```

- 工具：`internal/tools/todo.go`，`Capabilities{ReadOnly: true}`，schema `todos[]:{content,status}`，1–20 项。
- 面板：`internal/ui/todo_panel.go`，复用 `InteractionPanel`（标题 PLAN + Meta 进度 + 三态行）。
- 行为策略：`identity.md` 新增 Planning 段；`TestIdentityHasPlanningPolicy` 守卫。
- 注册：`BuildRegistry` 无条件注册；`buildBuiltinSpecs` 收录（`--list-tools`）。

三态样式：`[x]` completed（Success 标记 + Muted 文本）、`[~]` in_progress（Warning 标记 + Body 文本）、`[ ]` pending（Muted 标记 + Body 文本）。

## 5 测试

- `internal/tools/todo_test.go`：注册、只读能力、输出格式、校验（空/超限/非法 status/空 content）。
- `internal/ui/todo_panel_test.go`：面板三态渲染、Meta 进度、`TodoSummary`、`ToolOperationLabel`。
- `internal/app`：TTY 注入 display block 且抑制一行记录、非 TTY stderr 摘要、raw/minimal/hide-tool-status 门控。
- `internal/prompts`：identity 守卫 + 提示词体积预算回归。
- `internal/tooling`：BuildRegistry 注册、BuiltinSpecs 收录。

## 6 常驻 footer 进度行（2026-08-18 补充）

内联面板会随输出滚出视口。补充一个常驻单行进度，让当前计划始终可见：

- **状态**：`Mods.todoItems []ui.TodoItem`（会话内存活，遵循单 goroutine 契约）。
- **更新**：`handleToolCallsDone` 中 `todo_write` 成功即更新（展示与状态解耦）。
- **渲染**（`footerView`）：`ui.TodoFooterLine` 单行——进行中 `PLAN 1/3 · ▸ 当前步骤`；无进行中取第一个 pending；全完成 `PLAN 3/3 done`。置于 spinner/操作状态行上方；user-input/审批横幅接管 footer 时让位；门控同操作状态行（`showOperationStatus && !HideToolStatus`）。
- **轮边界**：`setupStreamContext` 开头清除"全部 completed"的计划（完成后保留到下一轮）；`--continue` 从历史消息倒序恢复最近一次 `todo_write` 计划，仅当其未全部完成（已完成计划不复现，未完成计划跨会话显示以支持续做）。

## 7 非目标

- 不做计划确认门（plan mode 式审批）。
- 不做 footer 常驻面板。
- 不新增 DB 表/迁移。
- 不为不支持工具的 provider 提供提示词降级方案。
