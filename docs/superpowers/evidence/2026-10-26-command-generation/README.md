# P0/P1：只读命令生成基线与提示对照

日期：2026-10-26。范围：P0 基线、P1 提示；P2–P5 未实施。

这是第一轮的历史记录。后续失败分析、提示精修及同批 P1/精修对照见 [精修验收记录](../2026-10-26-command-generation-refinement/README.md)。以下数字与快照保持原样，不代表最新提示的成绩。

本轮实现已完成。真实模型样本显示普通模式下日志计数有局部改善，minimal 模式没有重复该收益，原始代码行数任务仍失败或需要审批。因此目前不能宣称“不必要审批已稳定减少”，也不能把 P1 视为原问题的完整修复。

## 1. 实现与边界

生产修改仅为 `internal/prompts/prompts.go` 的 `ToolSelectionGeneral` 和 `ScriptPolicy` 两个片段：

- 第一次生成就优先简单只读查询，避免查询附带写文件、安装或创建脚本。
- 保持相关读取/聚合合并，优先字面输入和多操作数读取，不按文件机械拆调用，不为省调用嵌套复杂逻辑。
- 只做用户需要的输出；总量与明细复用数据，继续要求工具计算，不能心算代替聚合。
- 必要修改仍需完成且目标明确；构建、测试和脚本不能因为用途被称为只读。
- 有条件允许一次批量能力查询；保留失败纠正、完整性说明和必要复杂脚本的普通审批。

共享脚本指导通过现有工具描述同时到达 normal/minimal。未修改工具 schema、identity/workflow、分析器、授权规则、reviewMode、分发、审批 UI、自动简化或配置。提示预算保持原上限，没有通过增大预算容纳文本。

P0 新增内容：

| 文件 | 用途 |
| --- | --- |
| `internal/evals/testdata/command_generation/cases.json` | 14 类场景的输入、独立正确性要求、只读可解标记和解析正负例 |
| `internal/evals/testdata/command_generation/original.ps1` | 用户原命令，只解析，绝不执行 |
| `internal/evals/command_generation.go` | 合成文件、数值答案检查、区分未知观察的指标 |
| `internal/app/command_generation_baseline_test.go` | 使用真实分析与授权适配入口，独立断言先于快照；冻结提示替换及源码指纹检查 |
| `internal/app/command_generation_prompt_test.go` | 实际 normal/minimal 提示组装检查 |
| `internal/app/command_generation_integration_test.go` | 生产上下文、工具注册、provider 与审批调用链的 A/A1 对照 |
| `internal/app/testdata/command_generation/` | 旧提示、环境/政策哈希、新 Windows 观察 |

原有黑盒测试保持不变。新增对照放在 app 包，是为了仅替换两段提示并直接观察 `errReviewUnavailable`；不是通过模拟审批器放行工具，也不是 CLI 交互测试。原有工作树修改清单已保存在 `environment_before.json`，本轮没有覆盖它们。

## 2. 实验设计与统计口径

- HEAD：`33dd565d38e241afd31b2937e661a341ea12cb67`；Windows amd64，Go 1.26.3，实际 PowerShell 7.4.14。
- 模型：`deepseek-v4-flash`，`https://api.deepseek.com/`，temperature 0，think false，max_tokens 4096。没有模型 seed，temperature 0 也不保证完全确定。
- A：冻结的旧 `ToolSelectionGeneral`/`ScriptPolicy`；A1：当前两个片段。相同 schema、工具集和分析/审批代码。
- 固定政策指纹：`589d17b85f4fdf2ad4b81bf62e2a606da95ac17bfddc4df50bc3ccd56bb71049`。每次实验先验证清单内分析/授权生产源文件的 SHA-256 未变。
- 每个场景每组 5 次，配对使用相同目录和内容，组间重置夹具；顺序交替 A/A1、A1/A。会话冷启动，无保存规则、自定义只读信任、项目指令、安装 skills、MCP 或 Web 搜索。
- normal 首轮：14 场景 × 5 次 × 2 组 = 140 个响应，只分析模型输出，**不调用 CallTools，不执行任何生成命令**。
- normal/minimal 完成测试：每模式 4 场景 × 5 次 × 2 组 = 40 次，共 80 次有界会话。最多 6 个工具轮次、每会话 2 分钟，原生生产 caller、非交互 auto。unknown 必须阻断，不自动批准。
- 完成测试仅执行合成目录中的四个查询任务；构建、下载、现有脚本及恶意解析控制不进入执行实验。

数值任务有预先固定的独立答案：Go 文件 3 个/物理行 6 行、CSV 总和 10、ERROR 行 2。夹具含空文件、空行、CRLF、路径空格和无末尾换行；Go Scanner 单独验证物理行 oracle。还检查原文件内容及文件总数不变。

`completed` 只表示模型结束，不等于正确。`zero_review_completion` 要求查询正确、完成、没有实际审批、没有 unavailable、没有已识别写调用。第一轮无完成证据时为 null；含糊原请求的自动正确性也为 null，需人工复核。字段 `unexpected_writes` 统计已识别写调用尝试，不是完整的系统副作用审计。

非交互实际显示的审批数都是 0；**审批负担用生产 caller 返回的 `approval_unavailable` 单独统计**。不能用 UI 显示数 0 宣称模型无需审批。`predicted_decision` 是离线诊断，不能当成执行授权：观察器未完整复刻 process 可执行文件绑定或逐调用 cwd 重绑定；实际阻断数取生产 caller。脚本执行没有绕过审批。

## 3. 首轮结果（normal）

只比较查询场景，排除必要写入、构建和现有脚本，以免把“尚未做必要工作”误算成审批改善。

| 指标 | A | A1 |
| --- | ---: | ---: |
| 查询场景响应数 | 50 | 50 |
| 首轮包含预计 ask 的响应 | 10/50 | 8/50 |
| 首轮工具调用总数 | 65 | 62 |
| 诊断为读取的调用 | 55 | 54 |
| 静态证明读取的命令调用 | 29/39 | 26/34 |
| 原生读取契约调用 | 26 | 28 |
| 标记 opaque 的调用 | 14 | 10 |

分母 50 包含 10 条查询场景；其中 hidden-write/nested-code 两条解析安全控制共用同一个“统计 Go 文件数量”用户提示，不是独立的新任务种类。静态只读、原生读取与 opaque 是不同维度，不能互相代替。

改善主要来自日志计数（预计 ask 5→3）和相关 Git 读取（调用 10→7）。原始含糊请求首轮通常只列目录，因此其首轮 ask 为零不说明后续能免审完成。必要编辑、测试、下载、已有脚本仅有首轮观察，不能据此宣称必要工作全部完成；它们的减少审批数不计入上述收益。

## 4. 完成结果

下表的分母都是每组 5 次。正确完成率只对三个明确数值任务自动判断；原始请求另行复核。调用数/轮次均为这 5 次合计。

| 模式 / 任务 | 正确完成 A→A1 | 正确且无审批阻断 A→A1 | unavailable A→A1 | 调用 A→A1 | 工具轮次 A→A1 |
| --- | --- | --- | --- | --- | --- |
| normal / 物理行数 | 0/5→0/5 | 0/5→0/5 | 5→5 | 45→45 | 25→25 |
| normal / CSV 求和 | 5/5→5/5 | 5/5→5/5 | 0→0 | 5→5 | 5→5 |
| normal / ERROR 计数 | 5/5→5/5 | 0/5→3/5 | 5→2 | 14→11 | 14→11 |
| minimal / 物理行数 | 0/5→0/5 | 0/5→0/5 | 5→5 | 45→45 | 25→25 |
| minimal / CSV 求和 | 5/5→5/5 | 5/5→5/5 | 0→0 | 5→5 | 5→5 |
| minimal / ERROR 计数 | 5/5→5/5 | 3/5→2/5 | 2→3 | 12→12 | 12→12 |

这三个预登记数值查询的无审批阻断正确完成：normal 为 **5/15→8/15**；minimal 为 **8/15→7/15**。失败全部保留在各自分母中。合并两个模式是 13/30→15/30，但模式分布不同且每格只有 5 次，不据此宣称稳定改善或统计显著。

四查询完成实验共 80 次：未观察到已识别写调用或 API 传输错误；10 次到达工具轮次上限，均来自原始含糊代码统计请求。测试 PASS 只表示评测流程/断言通过，不代表模型质量通过。

### 原始“统计代码行数”

| 模式 / 组 | 结束回答 | 轮次上限失败 | unavailable | 调用总数 | 轮次总数 |
| --- | ---: | ---: | ---: | ---: | ---: |
| normal / A | 0/5 | 5 | 5 | 64 | 30 |
| normal / A1 | 1/5 | 4 | 5 | 61 | 29 |
| minimal / A | 4/5 | 1 | 5 | 44 | 25 |
| minimal / A1 | 5/5 | 0 | 5 | 46 | 25 |

每次都至少需要一次审批，因此这些任务没有任何免审批成功；没有把自动正确性 null 当成成功。人工复核：normal A1 第 4 次的文件数、物理行数及分类合计正确，但包含自行增加的详细表格；minimal A1 第 1 次的物理行数与非空行数正确，其余回答有误计、未说明口径或分项/合计不一致。minimal A1 第 3 次输出 4 行恰好等于非空行数，但未声明口径，不判为明确正确。minimal A 各已完成回答均有数值或自洽问题。上述均为本次人工样本观察，不回填为自动 oracle。

### 留存的失败

1. 明确物理行数任务，两组仍生成赋值、双层 foreach、字节读取和累加的脚本，生产审批阻断；之后逐文件读取/查元数据，最终把 6 行误答为 5。提示并未阻止复杂生成，也没避免调用膨胀。
2. 日志查询经常套上 `if (Test-Path ...) { ... } else { ... }`。普通模式 A1 有部分调用变为简单读取，但 minimal 未稳定重现。
3. CSV 都直接读取文件后给出 10，数值正确，却没有工具聚合证据。因此数字正确不代表满足“在工具中计算”的流程要求，更不能外推大数据量准确性。
4. 基线中 `Import-Csv ... | Measure-Object -Property amount -Sum` 仍为 ask；同一数据可用已支持读取工具读取。本轮将该限制明确记录，没有给 Import-Csv 新增放行规则。

## 5. 验证结果

| 检查 | 实际结果 |
| --- | --- |
| `go test ./internal/prompts ./internal/evals -count=1` | PASS |
| `go test ./internal/app -run 'Test(CommandGenerationBaseline\|GenerationPrompt\|ReadOnlyGenerationGuidance\|AssembledPromptBudgets)' -count=1` | PASS，12.8 秒 |
| `go test -tags integration ./internal/app -run '^$'` | PASS，集成测试编译 |
| `go test ./internal/app -run '^TestCommandAnalysisBaseline$' -count=1` | PASS，9.0 秒 |
| `go test -tags commandtrace ./internal/app -run '^TestCommandAnalysisWorkBaseline$' -count=1` | PASS，11.4 秒 |
| `go run github.com/go-task/task/v3/cmd/task@v3.51.1 check` | PASS，`go build ./...` |
| `go run github.com/go-task/task/v3/cmd/task@v3.51.1 test` | PASS，根模块全部测试，app 51.7 秒；见 root-test.log |
| normal 首轮真实 A/A1 | 流程 PASS，140 响应，157.6 秒；质量结果见上 |
| normal 完成真实 A/A1 | 流程 PASS，40 会话，203.0 秒；质量结果见上 |
| minimal 完成真实 A/A1 | 流程 PASS，40 会话，198.7 秒；质量结果见上 |
| 增补物理行 oracle/实际审批非零指标测试后的 `go test ./internal/evals -count=1` | PASS，5.6 秒 |

Windows 观察包含实际 PowerShell bridge 和 POSIX 语法分析，但后者不是 Linux 执行证据。未做 PS 5.1、Linux/macOS 实机、TTY 审批 UI/人工计时、多模型或更大真实仓库验收；没有把交叉编译计作通过。没有触及分析器，无需为本轮新增语义 fuzz 或修改旧快照。

## 6. 回放

离线无需 API key：

```powershell
go test ./internal/evals ./internal/prompts ./internal/tools -count=1
go test ./internal/app -run 'Test(CommandGenerationBaseline|GenerationPrompt|ReadOnlyGenerationGuidance|AssembledPromptBudgets)' -count=1
```

真实模型需要 `DEEPSEEK_API_KEY`。首次首轮对照不执行工具；完成对照使用临时合成目录和生产非交互审批。报告目录请换成新的路径，避免覆盖留存结果：

```powershell
$env:MODS_GENERATION_REPEATS='5'
$env:MODS_GENERATION_REPORT_DIR=Join-Path $PWD 'generation-rerun-normal'
go test -tags integration ./internal/app -run '^TestReadOnlyGenerationFirstRoundIntegration$' -count=1 -timeout=15m -v
go test -tags integration ./internal/app -run '^TestReadOnlyGenerationCompletionIntegration$' -count=1 -timeout=15m -v
$env:MODS_GENERATION_MINIMAL='1'
$env:MODS_GENERATION_REPORT_DIR=Join-Path $PWD 'generation-rerun-minimal'
go test -tags integration ./internal/app -run '^TestReadOnlyGenerationCompletionIntegration$' -count=1 -timeout=15m -v
```

每份 JSON 保留 case/arm/repeat、模型参数、政策和完整组装输入指纹、命令原文、分析诊断、答案、耗时、usage、调用/轮次、审批阻断、错误及正确性。usage 是 provider 报告值，不用字节数估算 token。提示含临时路径和系统状态，单次 fingerprint 不用于宣称不同会话字节相同；配对目录相同。固定源文件另记在 `implementation.json`。

后续 P2+ 会修改政策源文件，当前 P0/P1 指纹检查届时应明确迁移到独立实验组，不能悄悄重写历史清单。已保存数据仅含合成任务和本机临时路径，不含密钥。

## 7. 本阶段决策

保留简短只读生成指导及可回放基线，把效果标为“局部改善、尚未达成总体目标”。原任务依然需要后续工作；本轮不因样本不足或失败而放宽分析/授权，也不启用简化或修改审批界面。后续若继续，应使用同一批失败样本检验原因和收益，继续将正确性、必要工作完成及调用数量与审批负担一起核验。
