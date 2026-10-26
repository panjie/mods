# P2a/P2b 交接说明（handoff）

日期：2026-10-26。基线提交：`d34fe20`。本轮只实施 P2 首批，未改提示词、审批界面、规则匹配、审批策略或简化/重试机制。

## 当前状态

- P0/P1 已提交。P2a（结构诊断）与 P2b 首批（Import-Csv 绑定 + ForEach 参数收紧）已实现，随本提交一并入库。
- P3/P4/P5 未开始。计划文档 `docs/superpowers/plans/2026-10-26-reviewable-command-generation.md` 的状态行已同步。
- 详细支持表、52 条重放对照与安全说明见同目录 `README.md`。

## 已验证（本机 Windows，提交前）

- `go build ./...` 通过。
- `go test ./internal/commandanalysis ./internal/approval -count=1` 通过；`internal/commandanalysis` 的 4 个 P2 专项测试全部 PASS（`TestP2PowerShellReadAggregation` 覆盖 PowerShell 5.1 与 7，`TestP2PowerShellStructure`、`TestP2ImportCSVBinding`、`TestP2ForEachParameterBoundary`）。
- `internal/app` 的 `TestCommandGenerationP2Replay`、`TestCommandGenerationP2AccessBoundaries`、`TestCommandGenerationBaseline`、`TestCommandAuthorizationComparison*` 通过。
- `go test -tags commandtrace ./internal/app -run '^TestCommandAnalysisWorkBaseline$'` 通过（每次 Analyze 仍只做主解析一次）。
- 三个 stage-6 fuzz 各 10s、parallel=2 均 PASS。
- `baseline_p2_windows.json` 用 `-update-command-baseline` 重生成后与入库版本逐字节一致，无未记录漂移。
- 52 条重放：before 19 ask → structure 19 ask → after 16 ask，变化仅为 minimal `csv-sum` 三条。

## 已知未决问题（重要）

`TestAssembledPromptBudgets/normal/both` 失败：system_bytes 6164 > 上限 6144。

- **与 P2 无关**：在干净 `d34fe20` 上用 `git stash`（含 untracked）复现了完全相同的失败，随后已完整恢复工作区。
- 成因指向 P1 提示词精修膨胀，需要在 `internal/app/prompt_budget_test.go` 与提示文案之间做取舍。
- 后果：`task check` 能过，但 `task test` / `task ci` 会因此变红。这是回家后第一件该处理的事。
- 修复原则（见计划 §3）：只在确有必要且附分项解释时调整预算，不因文案膨胀直接放大总上限。

## 回家后建议的下一步

1. 先修 `TestAssembledPromptBudgets`，让主分支测试回到全绿。
2. 继续 P2b 后续项，按计划 §4 推荐顺序：静态属性过滤/投影 → 分组和计数 → 单层逐项读取 → 局部纯表达式。每项都要先确认现有能力，已支持的只补回归。
3. 每项 P2b 扩展需交付：精确语法/参数/来源支持表、独立正例、同结构插入写入与未知命令的负例、真实 bridge 输入 + Go 组合测试、catalog 修订与快照差异说明。
4. 不要用放行任意循环或 .NET 脚本的方式解决「复杂行数脚本」问题；该原始任务仍未解决（normal 模式 13 条 ask 一条未减）。

## 约束提醒

- 结构诊断（`CommandAssessment.Structure`）仅用于解释，绝不能作为授权、拒绝或重试依据。
- 不要引入 KnownDirs 回流授权；不要丢失非 nil 空写组；不要通过临时路径或候选提升权限。
- 不许诺 Linux/macOS 已运行；本轮只有 Windows 证据。
- P0/P1 实时模型评测脚本带旧分析器哈希保护，不能在 P2 代码上运行并声称「只改了提示词」。

## 新增/修改文件索引

新增：

- `internal/commandanalysis/structure.go`、`structure_test.go` — P2a 结构事实
- `internal/commandanalysis/powershell_csv.go`、`powershell_csv_test.go` — P2b Import-Csv 绑定
- `internal/commandanalysis/p2_aggregation_windows_test.go` — 聚合正负例（真实 bridge）
- `internal/app/command_generation_p2_test.go` — 重放与访问边界
- `internal/app/testdata/command_generation/baseline_p2_windows.json`
- `internal/app/testdata/command_generation/p2_replay_{before,structure,after}_windows.json`
- `docs/superpowers/evidence/2026-10-26-command-generation-p2/README.md`、`HANDOFF.md`

修改：

- `internal/commandanalysis/assessment.go` — 挂载 `Structure`
- `internal/commandanalysis/result.go` — `Clone` 深复制 `Structure`
- `internal/commandanalysis/catalog.go` — `readPSForEach` 参数收紧
- `internal/commandanalysis/catalog_entries.go` — 新增 `import-csv` rev 1，`foreach-object` 升至 rev 2
- `internal/commandanalysis/stage6_properties_test.go`、`internal/app/command_generation_baseline_test.go`、`command_generation_windows_test.go`、`command_targets_test.go` — 断言与快照路径
- `docs/superpowers/plans/`、`docs/superpowers/specs/` 对应计划与设计文档状态行
