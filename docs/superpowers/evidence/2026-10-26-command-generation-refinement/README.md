# 失败样本分析与 P1 提示精修

日期：2026-10-26。仅修改生成提示、测试和评测记录；命令分析、审批和执行机制未改动。

结论：本轮对照中，日志计数和 minimal 代码统计有所改善，但没有消除复杂生成。normal 物理行数仍失败，CSV 聚合还出现额外审批。保留这些反例，不把小样本结果描述为稳定产品保证。

## 1. 失败原因：证据与推断

### minimal 没有收到完整的生成指导

`internal/app/stream.go` 在 minimal 模式跳过 identity、workflow、临时目录指导；`tool_selection.go` 的 `injectToolSelectionPrompt` 也直接返回。上一轮新增的“工具计算总量、不按文件拆调用、不增加报表、一次条件式能力查询”等主要位于 `ToolSelectionGeneral`，因此没有进入 minimal。仅 `ScriptPolicy` 通过工具描述到达 minimal。

这是组装代码和实际组装测试确认的事实。不能据此说 minimal 完全没有指导：脚本片段确实存在，但并没有完整表达聚合决策，尤其没有制止读文件后凭文本或文件大小计数。

### 模型把一个总量任务扩成报表程序

旧样本 `normal/ambiguous-code/A1/1` 先列两层目录，然后自行生成每文件 Lines、Bytes、相对路径及 Format-Table。这些用户未要求的字段引入赋值、嵌套聚合和自定义对象，进而进入 unknown 审批。之后又逐文件读取、查元数据、读取非代码文件，最终触及轮次上限。

对明确的物理行数请求，旧样本 `normal/physical-lines/A1/1` 从第一步就生成文件数组、字节数组、双层 foreach、换行判断和累加器。原提示只说“简单查询用简单命令”，没有给出“不同指标可用几个直接管道、最终 JSON 放在回答中组织”的优先顺序。模型为何选择该形式无法直接观察；“复杂度来自输出组织和对行语义的担心”是由上述结构作出的解释，不是模型内部推理的实测。

### 统计口径错误导致复杂补救

真实 PowerShell 7.4.14 与合成文件验证：

| 操作 | 结果 |
| --- | ---: |
| 枚举 `.go` 文件后 `Measure-Object` | 3 个文件 |
| 将各文件 `Get-Content` 的逐行记录送入默认 `Measure-Object` | 6 个物理行 |
| 相同输入使用 `Measure-Object -Line` | 4 个非空行 |

夹具包括空文件、空行、CRLF、无末尾换行及带空格路径。新的 `TestGenerationPowerShellAggregationSemantics` 在实机执行固定只读管道核验，独立 Go Scanner oracle 也得到 6。没有执行模型生成的未知脚本。

`fs_read_file` 默认读取原始文本，没有丢弃夹具空行；工具结果转为消息时也没有统一 TrimSpace。因此旧样本“正确应为 6、却答 5”不能归因于文件工具删除空行。样本显示模型在 shell 被阻断后转为逐文件读取/查大小并自行计数，错误出现在该路径。

修订期间还观察到：模型先用 `-Line` 得到 4，再怀疑末行处理，转为 `.NET`、正则和字节循环，产生更多审批。减少首轮复杂度仍可能在后续纠错中反弹，不能只测第一轮。

### 只读意图不等于当前分析器可证明

旧基线已经记录 `Import-Csv ... | Measure-Object -Property amount -Sum` 为 ask。本轮强化“用工具计算”后，CSV 的部分样本开始选择该命令，数值仍正确，但新增审批。相比读取 CSV 后模型心算，这更遵守工具计算指导，却暴露现有语义覆盖限制。本轮没有修改该限制，也没有教模型关闭审批。

## 2. 最终修改

生产修改都在 `internal/prompts/prompts.go`：

- 新增简短 `QueryPolicy`：先尝试少量直接读取/聚合管道，合并指标需要状态时可分开计算，只输出请求结果，不凭显示文本或文件大小算总量，不给普通读取套预检查。
- normal 的 `ToolSelectionGeneral` 和 minimal 的 `Minimal` 共用这一段。minimal 仍保留原输出样式，未注入完整 identity/workflow 或工具选择章节，也未修改组装分支。此处是有意补充少量查询决策提示，不能再称 minimal “只有输出样式指令”。
- `ScriptPolicy` 同时指导两种模式使用管道，保留相关读取合并和必要脚本路径；不以大量逐文件调用换取更短命令。
- `PowerShellIntentGuidance` 给出通用单层路径→读取映射，并直接区分默认 Count 与 `-Line`；每指标直接管道，最终 JSON 在回答中组织。
- 必要修改、构建/测试可能有副作用、错误纠正和不完整覆盖说明仍保留。没有统计 Go 的硬编码流程、固定答案、扩展名白名单或模型白名单；identity/workflow 未增加计数程序。

初稿曾超过原有描述字节预算，随后压缩措辞；未扩大预算。最终实际组装测试验证 normal/minimal 都恰好有一份系统层 QueryPolicy，且命令描述保留聚合指导与 PowerShell 计数语义。

测试变动包括真实 PowerShell 行语义、两模式实际组装、冻结提示替换不改变用户输入/schema，以及更新三个引用旧文案的提示契约断言。它们没有修改安全期待。

## 3. 对照设计及版本

本轮 **A 是上一轮 P1，A1 是本轮精修**。不把上一轮 A1 的历史成绩与本轮 A1 直接相减。

- 新增冻结快照 `internal/app/testdata/command_generation/prompts_p1.json`，涵盖 general、script、PowerShell、minimal 四段。
- `MODS_GENERATION_BASELINE=p1` 选择本轮基线；省略或 `original` 仍支持最初 P0 提示。原 `prompts_before.json` 和原结果未覆盖。
- 报告新增 `baseline_revision`。两个实验臂仅替换提示文字，使用同一模型、schema、工具注册、合成内容和生产 caller。
- Windows amd64，Go 1.26.3，PowerShell 7.4.14，DeepSeek `deepseek-v4-flash`，temperature 0、think false、4096 输出 token 上限。每关键场景每组 5 次，交替顺序，配对路径相同、内容重置，最多 6 个工具轮次。
- 非交互 auto，没有新只读信任或保存规则。64 个被冻结的分析/审批生产源文件 SHA-256 检查保持一致，政策指纹仍为 `589d17b85f4fdf2ad4b81bf62e2a606da95ac17bfddc4df50bc3ccd56bb71049`。

所有迭代均留存，正式结果没有用某次较好样本替换：

| 数据目录 | 提示版本/用途 | 规模 |
| --- | --- | ---: |
| `pilot/` | 仅描述精修；数字循环缩短但仍阻断 | 6 次完成试验 |
| `pilot2/` | 描述加入直接管道、JSON 与映射；仍出现循环 | 4 次完成试验 |
| `pilot3/` | 补系统 QueryPolicy 的 minimal 探索 | 6 次完成试验 |
| `normal/`、`minimal/` | `prompts-round3.json`；五次重复的完整中间对照 | 首轮 280 响应 + 完成 80 会话 |
| `final-normal/`、`final-minimal/` | `prompts-final.json`；将物理行 Count 指令写得更直接 | 完成 80 会话 + 首轮边界检查 56 响应 |

pilot 的修改片段见 `pilot-fragments.json`，其余片段沿用 P1；pilot3 使用 round3 提示。最终首轮边界检查每场景每臂仅一次，只验证没有明显首轮异常，不是五次重复的质量结论。主结论来自最终每格 5 次完成对照。先前中间对照出现 normal 物理行正确 2/5、minimal 2/5，最终变为 0/5、3/5，体现了小样本波动，不能断言最后一句修改单独造成提升。

## 4. 最终完成结果

数值任务按预先固定的 JSON oracle 判断；错误、未结束和轮次上限全部保留在分母。下表每格均为 5 次，调用/轮次和 unavailable 是五次合计。

| 模式 / 任务 | 正确 A→A1 | 正确且无审批阻断 A→A1 | unavailable A→A1 | 调用 A→A1 | 轮次 A→A1 |
| --- | --- | --- | --- | --- | --- |
| normal / 物理行数 | 0/5→0/5 | 0/5→0/5 | 5→8 | 46→46 | 26→28 |
| normal / CSV | 5/5→5/5 | 5/5→5/5 | 0→0 | 5→5 | 5→5 |
| normal / ERROR | 5/5→5/5 | 0/5→5/5 | 5→0 | 13→5 | 13→5 |
| minimal / 物理行数 | 0/5→3/5 | 0/5→3/5 | 5→2 | 47→18 | 26→12 |
| minimal / CSV | 5/5→5/5 | 5/5→2/5 | 0→3 | 5→8 | 5→8 |
| minimal / ERROR | 5/5→5/5 | 2/5→5/5 | 3→0 | 13→5 | 13→5 |

三个明确数值任务的免审批正确完成：**normal 5/15→10/15；minimal 7/15→10/15**。调用总数分别为 64→56、65→31。不能只引用这两个总率：normal 物理行的审批和轮次变坏，minimal CSV 出现额外审批，分别是本轮明确的反效果。

原始含糊请求不自动规定必须输出物理行，继续保留自动 correct=null，另作人工复核：

| 模式 | 结束回答 A→A1 | unavailable A→A1 | 调用 A→A1 | 轮次 A→A1 | A1 正确且无阻断（人工复核） |
| --- | --- | --- | --- | --- | --- |
| normal | 2/5→5/5 | 5→5 | 58→52 | 28→25 | 0/5 |
| minimal | 4/5→4/5 | 5→1 | 47→24 | 26→20 | 4/5 |

minimal A1 第 2–5 次均有三个文件各 3/0/3、合计 6 的自洽结果；第 3、5 次还给出正确非空行总量 4。第 1 次达到轮次上限并遇审批，计失败。normal A1 第 1、3、4 次数字正确，但全部有审批阻断且依赖逐文件读取后的汇总；第 2、5 次数字或表内口径冲突。基线 A 两模式每次都至少一次阻断，故原始请求均无免审批成功。人工复核未改写原 JSON 中的 null，也未与数值 oracle 合并分母。

最终 80 个完成会话没有 API 传输错误或已识别写调用；10 次达到轮次上限（normal 7、minimal 3），全部保留。unavailable 是生产 caller 的真实阻断，实际 UI 审批展示数为 0，因为运行环境非交互；不能把 0 次展示解释为没有审批需求。

最终首轮边界检查（每场景一次）中，每模式查询响应各臂 10 个：normal 首轮预计 ask 调用 2→0，调用数 13→14；minimal 为 ask 2→0、调用 14→15。首轮更清楚仍不保证完整任务免审，normal 物理行就是反例。必要修改/构建/下载/已有脚本只分析首轮，不执行、不计为“完成必要工作”的证据。

## 5. 留下的问题和解释边界

1. **提示遵循仍不可靠。** 即使写出 Count，模型仍会选择 `-Line`、加入逐文件对象或改写为字节循环。最终 normal 物理行没有成功，不能宣布原任务已修好。
2. **两种模式的上下文不同。** 缺失 QueryPolicy 已补齐，但 normal 还有完整工作流、工具选择和临时目录指导。没有逐条消融这些差异，不能确定剩余差距来自哪一段，也不为实验删除完整模式的安全指导。
3. **“可证明读取”与“简单命令”不是同一指标。** 某些自定义对象/赋值读取在现有分析器下已能证明读取，仍可能繁琐；部分输出仍自行增加分类表格。审批减少不等于审阅复杂度全部解决。
4. **聚合要求与现有工具覆盖有冲突。** minimal CSV 的 3 次额外审批来自 Import-Csv 聚合；不能通过放宽授权或让模型心算掩盖它。normal CSV 全部直接读取后作答，数值正确仍不证明满足工具聚合要求。
5. **试验边界。** 单模型、单 Windows host、很小的合成目录；无固定模型 seed。没有统计显著性、交互 UI 用户审批次数、实际大仓库、PS5.1/Linux/macOS 或多模型证据。`predicted_decision` 仍为诊断；实际授权取生产 caller，不借预测结果执行。

## 6. 验证与回放

最终 `go run github.com/go-task/task/v3/cmd/task@v3.51.1 check` 和 `test` 均通过。根模块当前最终日志为 `root-test-final-current.log`。期间失败是三个引用旧措辞的提示契约断言及初稿描述预算；已更新文案断言并压缩文本，没有降低授权断言或放大预算。

`TestGenerationPowerShellAggregationSemantics`、`TestReadOnlyGenerationGuidanceReachesNormalAndMinimal`、两种冻结基线替换测试与 `TestAssembledPromptBudgets` 均通过；最终定向 app 检查 19.9 秒。最终真实完成对照 normal 206.5 秒、minimal 193.9 秒，流程 PASS 与模型正确率分开记录。

离线：

```powershell
go test ./internal/prompts ./internal/tools ./internal/config -count=1
go test ./internal/app -run 'Test(GenerationPrompt|GenerationPowerShell|ReadOnlyGenerationGuidance|AssembledPromptBudgets|CommandGenerationBaseline)' -count=1
```

真实对照需 `DEEPSEEK_API_KEY`，用新输出目录保存，避免覆盖本次结果：

```powershell
$env:MODS_GENERATION_BASELINE='p1'
$env:MODS_GENERATION_REPEATS='5'
$env:MODS_GENERATION_MINIMAL='0'
$env:MODS_GENERATION_REPORT_DIR=Join-Path $PWD 'generation-refinement-rerun-normal'
go test -tags integration ./internal/app -run '^TestReadOnlyGenerationCompletionIntegration$' -count=1 -timeout=15m -v
$env:MODS_GENERATION_MINIMAL='1'
$env:MODS_GENERATION_REPORT_DIR=Join-Path $PWD 'generation-refinement-rerun-minimal'
go test -tags integration ./internal/app -run '^TestReadOnlyGenerationCompletionIntegration$' -count=1 -timeout=15m -v
```

首轮对照入口为 `TestReadOnlyGenerationFirstRoundIntegration`，不执行工具。报告继续保存失败、完整命令、答案、模式、基线版本、usage、调用数、轮次和指纹。最终源码哈希在 `source-hashes.json`；提示全文在 `prompts-final.json`。此轮保留的是可继续检验的局部改善，不启动 P2 分析扩展或任何审批/简化机制。
