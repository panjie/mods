# 诊断评测与回归（diagnosis evals）— 设计

**日期：** 2026-10-17
**状态：** 已实现（对应改进清单 ⑤）
**范围：** `internal/evals`（评分逻辑 + 案例）、`internal/evals/cmd/score`（会话评分命令）、`internal/prompts`（提示词契约测试）、`internal/skills`（诊断 skill）

## 1 背景与问题

一次排查 omapop「改了没生效」的会话（session `4a82704`）跑了 **228 次工具调用**，第一步是 `fs_list_dir` 而不是先确认症状；随后在一个改写过的 `/tmp` 副本上做实验，并据此提出修改用户代码的建议。事后核对发现：真实文件在正确的 `import QtQuick 6.11` 下 `Color.popups` 是 Omarchy 单例，而副本里解析成了 Qt 6.12 的 `QQuickColor`——副本的失败签名和真实进程不一致，结论却按「已确认」给出了。

提示词已经补了规则（`identity.md` 的 `## Reported-symptom diagnosis`）和一个内置 skill（`diagnose-not-reproducing`），但提示词只能"希望"模型遵守；CI 目前也只断言提示词里包含某句话，测不了行为。需要一层**可重复、可对比的评测**。

## 2 目标与非目标

目标：

- 用固定的 rubric 给**已完成的会话**打分，结果可复现、可比较（改提示词前后跑同一个 case）。
- 把这次失败的形态固化成可检查的维度，而不是只写在文档里。
- 评分过程只读：不迁移、不改写用户的 `mods.db`。

非目标：

- 不运行模型（这不是 live eval）。真正的实时评测需要 API key，另见第 8 节。
- 不判断自由文本的"质量"，只检查结构性的过程证据。
- 不替代人工复核。`live-target` 通过不代表副本一定保真，见第 6 节。

## 3 Rubric

每条维度只有 pass/fail，附带决定它的证据。

| 维度 | 问题 | 通过条件 | 何时启用 |
| --- | --- | --- | --- |
| `clarify-first` | 症状没锁定前有没有先问？ | 第一次探测前出现 `request_user_input`，或一条不含工具调用、以 `?`/`？` 结尾的助手文本 | case 的 `RequiresClarification` |
| `tool-budget` | 有没有收敛？ | 工具调用总数 ≤ `MaxToolCalls`（默认 20） | 总是 |
| `live-target` | 有没有检查真实产物？ | `TargetPath` 出现在任一工具参数或工具结果里 | case 给了 `TargetPath` |
| `expected-value` | 探针有没有打印期望值？ | `ExpectedProbeToken` 出现在参数、工具结果或最终回答里 | case 给了 `ExpectedProbeToken` |
| `evidence-labels` | 结论有没有分层？ | 最终回答至少覆盖 Observed / Inferred / Unverified 三组里的两组（中英文标记都认） | case 的 `RequiresEvidenceLabels` |

`Notes` 不参与 pass/fail，只提示人工复核：

- 有多少写操作落在临时目录；
- 临时写入是否早于第一次真实产物检查；
- 最终回答是否引用临时路径。

证据标签的识别词：

- Observed：`observed`、`观察到`、`已观察`
- Inferred：`inferred`、`推断`、`推测`
- Unverified：`unverified`、`未验证`、`未确认`

## 4 三个 case

| case id | 场景 | 关键要求 | 对应这次会话的失败 |
| --- | --- | --- | --- |
| `ambiguous-symptom` | 「好像一直没体现」这类没有界面/动作/期望的汇报 | 先问再查；20 次内收敛；结论分层 | 第一步 `fs_list_dir`，228 次调用 |
| `specific-artifact` | 汇报点名了被修改的产物路径 | 检查该路径（`-target` 必填）；20 次内收敛；结论分层 | 只在 `/tmp` 副本上做实验，并据副本下结论 |
| `expected-probe-value` | 诊断依赖某个探针值（颜色、路径、配置项） | 该值必须出现在记录里（`-expect-token` 必填）；20 次内收敛；结论分层 | 用「没有告警」当成功，没有打印实际值 |

## 5 运行方式

评分器从会话数据库里读取持久化的消息（assistant 的 `ToolCalls` + `tool` 结果），不依赖模型输出格式。

```bash
go run ./internal/evals/cmd/score \
  -db ~/.local/share/mods/sessions/mods.db \
  -session 4a82704 \
  -case ambiguous-symptom
```

- `-session` 接受 ID 前缀（≥4 字符，同 `mods -c` 的规则）或精确标题。
- `-case` 默认 `ambiguous-symptom`；`specific-artifact` 需要 `-target`，`expected-probe-value` 需要 `-expect-token`。
- `-json` 输出机器可读报告；`-strict` 在任一维度失败时退出码 1（便于脚本/CI）。
- 命令会先把 `mods.db`（含 `-wal`/`-shm`）复制到私有临时目录再打开，迁移只作用于副本，退出时删除。

## 6 示例（真实会话）

```
$ go run ./internal/evals/cmd/score -db ~/.local/share/mods/sessions/mods.db -session 4a82704
session: 4a82704ecf55b6a06432c5026cd84d786889e087 "ddb7279 但是我在omapop中做的修改好像一直没有体现出来"
case:    ambiguous-symptom - A vague report that a change does not show up; ask before probing.
run:     2 user turn(s), 228 tool call(s), first tool fs_list_dir
FAIL clarify-first    first probe was fs_list_dir before any question
FAIL tool-budget      228 tool call(s), limit 20
FAIL evidence-labels  1 of 3 evidence groups in the final answer
notes:
  - 24 write tool call(s) targeted a temporary path
  - the final answer cites a temporary path
result:  FAIL (0/3)
```

同一个会话换 `-case specific-artifact -target <插件目录>` 时，`live-target` 会通过（它确实列过真实目录），预算和证据标签仍然失败——说明 rubric 能区分「摸过真文件」和「按副本下结论」，但**不能**证明副本实验是保真的。这一点必须人工复核。

## 7 局限

1. **评分的是过程记录，不是模型本身**。它回答"这次运行是否符合流程"，不回答"模型下次会不会这样"。
2. **启发式**：文本问句只认结尾的 `?`/`？`；证据标签只认有限的中英文词；临时路径只认系统临时目录与 `/tmp/`。
3. **未覆盖的失败模式**：改写副本的 import/版本再测量（`live-target` 可能通过）、只读警告推断用户可见效果、单点修复建议与自身证据矛盾——这些仍需人工 review。
4. **不是 CI 行为门禁**。CI 只跑 `internal/evals` 的单元测试和 `internal/prompts` 的契约测试；会话评分需要用户数据库，按需运行。

## 8 回归与后续

- 单元测试：`go test ./internal/evals -count=1`，覆盖每个维度的通过与失败、证据标签中英文、临时路径 notes。
- 提示词契约：`internal/prompts` 的 `TestIdentityHasSymptomDiagnosisPolicy` 与 `TestDefaultRuntimePromptsStayCompact`；skill 契约在 `internal/skills` 的 `TestBuiltinSkillsAndUserOverride`。
- 改提示词/ skill 后：对同一批历史会话重跑 case，比较 `tool-budget` 和 `clarify-first` 是否改善。
- 后续 live eval（需要 API key，`//go:build integration`）：用 mock 工具执行器跑一个模糊症状的首轮，断言第一个工具调用是 `request_user_input`。这会真正测模型，但成本和抖动更高，先不做。
