# P5 真实生成评测与发布决策

日期：2026-10-27。评测基线：`b906e77`（P4 已提交，默认 `command-simplification: off`）。A/A1 使用 `d34fe20`（P1 精修提交，pre-P2）的归档源码运行，以保持固定的分析器/授权版本。

状态：Linux 真实模型 A/A1/B/C 对照完成；Windows/macOS/PowerShell、人工审批界面和人工理解评分未执行。因此不能宣称 P5 的跨平台/人工验收完成，也不能据此改变默认值；默认值保持 `off`。

结论：**本轮 Linux 真实模型对照没有观察到 `once` 的净收益，默认值保持 `off`。** C 组 40 次完成会话（180 次总运行）中自动重组为 0；模型生成的复杂调用主要是单层 `process_run sh -c` 或简单 `find | wc -l` 管道，未命中 P4 的窄候选门槛。物理行数任务在所有组仍为 0/5 正确，原因是生成了只读但语义错误的 `wc -l`，不是审批或自动重组能够修复的问题。

本记录只代表 Linux amd64、非交互、DeepSeek `deepseek-v4-flash`、合成夹具的样本观察。没有真实 Windows PS5.1/PS7、macOS、Linux PowerShell、人工审批界面或人工理解评分；这些项目明确未验证。P4 单元测试通过不能替代本记录中的真实模型结果。

## 1. 对照与固定条件

| 组 | 实现 | 说明 |
| --- | --- | --- |
| A | `d34fe20` + `prompts_before.json` | P1 之前的生成指导；分析器/授权与 A1 相同（pre-P2） |
| A1 | `d34fe20` + P1 精修提示 | 只改生成指导，分析器/授权与 A 相同 |
| B | `b906e77` + P5 评测接线，`command-simplification: off` | 提示 + P2 事实/窄语义 + P3 UI/载荷；自动简化关闭 |
| C | 同 B，`command-simplification: once` | 检查请求级一次自动重组的实际净收益 |

固定条件：

- 模型：`deepseek-v4-flash`，`https://api.deepseek.com/`，temperature 0，max_tokens 4096，think false。
- 模式：normal 为主组，minimal 为对照；`review-mode: auto`，非交互（`InteractiveTTYAvailable=false`），raw 输出。
- 夹具：`internal/evals/testdata/command_generation/cases.json` 的 14 类场景，使用 `evals.CommandGenerationFiles()` 的合成目录；每次重复重建，不读取用户文件。
- 重复：每个场景每组 5 次；首轮 140 次/组/模式，完成 40 次/组/模式；A/A1 与 B/C 合计 720 次运行。
- 顺序：同一次重复内 B/C 或 A/A1 交替；不同重复之间交替先后。
- 隔离：无项目指令、无安装 skills、无 MCP、关闭 Web 搜索、无保存审批规则、无额外只读信任。
- 策略指纹：A/A1 `589d17b8…71049`；B/C `885aba0c…e7d`。B/C 的 manifest 见 `internal/app/testdata/command_generation/p5_policy.json`，包含 90 个生产源文件及 5 段提示文本的 SHA-256；它覆盖命令分析、授权、app 审查/组织、配置、提示、工具指导和评测指标相关文件，不是完整仓库哈希。任何相关源码变化会使 P5 测试拒绝复用旧评测。
- 秘密：`DEEPSEEK_API_KEY` 从 `~/.bashrc` 载入测试进程，未写入本目录、日志或报告。

A/A1 与 B/C 都是本次 Linux 重跑，**不混用** P0/P1 的历史 Windows 数字。A/A1 报告来自 pre-P2 归档，B/C 报告来自当前工作树；两者的提示/策略指纹分别记录在原始 JSON 中。

## 2. 方法

### 2.1 A/A1

`d34fe20` 的 `command_generation_integration_test.go` 通过 `generationPromptArm` 只替换 `ToolSelectionGeneral`/`Minimal`/`ScriptPolicy`/`PowerShellIntentGuidance`，其余 schema、分析器、授权和工具集相同。该 harness 在非交互 auto 下使用生产 caller；未知效果返回 `approval_unavailable`，不会执行。

为避免在 `/tmp` 中创建不可见的 worktree，本轮用 `git archive d34fe20 | tar -x` 导出到工作区临时目录 `.p5-a1`，运行后删除；没有修改历史提交，也没有把交叉编译当成平台验收。

### 2.2 B/C

新增 `internal/app/command_generation_p5_integration_test.go`：

- 明确创建 P4 请求级 `commandOrganizationState`，并调用生产 `toolResultCallerForOrganization`。旧 A/A1 harness 直接构造请求，不会创建该状态，因此不能直接用于 C 组；这是本轮评测接线的必要修复。
- 每次运行先调用 `generationObserve` 记录工具、效果、静态读取、预测审计决定和 `organization_reasons`（诊断，不是预算或授权事件），再调用生产 caller。
- C 组在每次 subtest 新建状态，绑定当前用户提示为来源上下文；B 组传 nil 状态。两者共用同一模型、schema、提示和夹具。
- 自动/人工重组只由 `evals.ReorganizationKind` 的本地类型错误计数；任意工具文本中的 JSON 不会触发。
- `approval_unavailable` 只统计生产 caller 的 `errReviewUnavailable`。
- `allow_path_errors` 统计“harness 刚观察到 allow 分类，生产 caller 仍返回非控制错误”的调用；它不是可信 dispatch 事件，不能当作实际执行失败数。`tool_errors` 是更宽的非控制错误计数。
- 首轮模式不调用 `CallTools`，只分析模型输出；完成模式在非交互 auto 下运行，未知/写入由生产授权阻断。

### 2.3 自动重组接线验证

`TestCommandGenerationP5AutomaticOrganizationWiring` 使用固定嵌套命令和模拟 registry，验证 C 组生产 caller 返回 typed `not_executed`、`Automatic=true`，且零 dispatch。它不使用模型输出，也不授予权限。日志：`verification/p5-policy-wiring.log`。

### 2.4 不执行的载荷

完成实验只运行合成目录中的四个查询任务。未命中审批的未知命令返回 `approval_unavailable`，不 dispatch；命中自动重组时返回 typed `not_executed`，原调用不执行；已证明只读的调用才可能执行。`never` 组没有运行，因为它会直接执行模型生成的未知命令，违反本轮的评测安全约束；`off/once × auto/always/never` 的确定性矩阵由 P4 单元测试覆盖。

## 3. 结果

完整机器可读统计见 [`summary.json`](summary.json)，表格源见 [`tables.md`](tables.md)。原始 JSON 在 `raw/`；表内 `n/a` 表示该组没有 P4 字段（A/A1 的代码早于 P4），不是 0。

### 3.1 首轮生成

| 组 | 模式 | 臂 | 运行 | 调用 | 静态读取 | 预测 ask | 原生读取 | opaque | 写调用 | 重组原因 | 错误 | 中位 ms |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| A/A1 | normal | A | 70 | 90 | 44 | 15 | 31 | 14 | 5 | n/a | 0 | 987 |
| A/A1 | normal | A1 | 70 | 93 | 47 | 15 | 31 | 14 | 5 | n/a | 0 | 1072 |
| A/A1 | minimal | A | 70 | 86 | 34 | 15 | 37 | 10 | 5 | n/a | 0 | 879 |
| A/A1 | minimal | A1 | 70 | 89 | 41 | 15 | 33 | 10 | 5 | n/a | 0 | 925 |
| B/C | normal | B | 70 | 95 | 48 | 16 | 31 | 15 | 5 | 0 | 0 | 988 |
| B/C | normal | C | 70 | 90 | 45 | 15 | 30 | 15 | 5 | 0 | 0 | 995 |
| B/C | minimal | B | 70 | 89 | 42 | 17 | 30 | 10 | 5 | 0 | 0 | 983 |
| B/C | minimal | C | 70 | 87 | 44 | 16 | 27 | 10 | 5 | 0 | 0 | 972 |

首轮没有稳定改善：A→A1 在 normal 的静态读取 44→47、ask 15→15；A1→B 在 normal 的静态读取 47→48、ask 15→16；minimal 的 ask 也没有下降。B/C 每个臂的 5 个写调用全部来自 `download-batch` 的必要 `fs_mkdir downloads`，不是查询夹具的附带写入。

### 3.2 完成

| 组 | 模式 | 臂 | 运行 | 完成 | 正确 | 不正确 | 未判定 | 零审批成功 | 审批不可用 | 自动重组 | 人工重组 | 工具错误 | allow 后错误 | 调用 | 轮次 | 中位 ms |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| A/A1 | normal | A | 20 | 20 | 10 | 5 | 5 | 10 | 0 | n/a | n/a | n/a | n/a | 30 | 27 | 1834 |
| A/A1 | normal | A1 | 20 | 20 | 10 | 5 | 5 | 10 | 1 | n/a | n/a | n/a | n/a | 35 | 32 | 1843 |
| A/A1 | minimal | A | 20 | 20 | 10 | 5 | 5 | 10 | 0 | n/a | n/a | n/a | n/a | 30 | 30 | 1763 |
| A/A1 | minimal | A1 | 20 | 20 | 10 | 5 | 5 | 10 | 0 | n/a | n/a | n/a | n/a | 39 | 36 | 2010 |
| B/C | normal | B | 20 | 20 | 10 | 5 | 5 | 10 | 0 | 0 | 0 | 0 | 0 | 26 | 26 | 1941 |
| B/C | normal | C | 20 | 19 | 10 | 5 | 5 | 10 | 3 | 0 | 0 | 0 | 0 | 42 | 35 | 2042 |
| B/C | minimal | B | 20 | 20 | 10 | 5 | 5 | 10 | 0 | 0 | 0 | 0 | 0 | 30 | 30 | 1976 |
| B/C | minimal | C | 20 | 20 | 10 | 5 | 5 | 10 | 0 | 0 | 0 | 0 | 0 | 30 | 30 | 1797 |

“正确/不正确/未判定”针对四个完成场景：`csv-sum`、`log-count`、`physical-lines` 有自动数值 oracle；`ambiguous-code` 的 Correct 保持 null，需要人工复核。“零审批成功”同时要求正确、完成、无审批不可用、无已识别写调用；`ambiguous-code` 因此不计入。

### 3.3 按场景

| 组 | 模式 | 臂 | 场景 | 运行 | 正确 | 零审批 | 审批不可用 | 自动重组 | 调用 | 轮次 |
| --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| A/A1 | normal | A | ambiguous-code | 5 | 0/0/5 | 0/0/5 | 0 | n/a | 15 | 12 |
| A/A1 | normal | A1 | ambiguous-code | 5 | 0/0/5 | 0/0/5 | 0 | n/a | 13 | 12 |
| A/A1 | normal | A | csv-sum | 5 | 5/0/0 | 5/0/0 | 0 | n/a | 5 | 5 |
| A/A1 | normal | A1 | csv-sum | 5 | 5/0/0 | 5/0/0 | 0 | n/a | 6 | 6 |
| A/A1 | normal | A | log-count | 5 | 5/0/0 | 5/0/0 | 0 | n/a | 5 | 5 |
| A/A1 | normal | A1 | log-count | 5 | 5/0/0 | 5/0/0 | 0 | n/a | 5 | 5 |
| A/A1 | normal | A | physical-lines | 5 | 0/5/0 | 0/5/0 | 0 | n/a | 5 | 5 |
| A/A1 | normal | A1 | physical-lines | 5 | 0/5/0 | 0/5/0 | 1 | n/a | 11 | 9 |
| A/A1 | minimal | A | ambiguous-code | 5 | 0/0/5 | 0/0/5 | 0 | n/a | 14 | 14 |
| A/A1 | minimal | A1 | ambiguous-code | 5 | 0/0/5 | 0/0/5 | 0 | n/a | 19 | 17 |
| A/A1 | minimal | A | csv-sum | 5 | 5/0/0 | 5/0/0 | 0 | n/a | 5 | 5 |
| A/A1 | minimal | A1 | csv-sum | 5 | 5/0/0 | 5/0/0 | 0 | n/a | 8 | 8 |
| A/A1 | minimal | A | log-count | 5 | 5/0/0 | 5/0/0 | 0 | n/a | 6 | 6 |
| A/A1 | minimal | A1 | log-count | 5 | 5/0/0 | 5/0/0 | 0 | n/a | 5 | 5 |
| A/A1 | minimal | A | physical-lines | 5 | 0/5/0 | 0/5/0 | 0 | n/a | 5 | 5 |
| A/A1 | minimal | A1 | physical-lines | 5 | 0/5/0 | 0/5/0 | 0 | n/a | 7 | 6 |
| B/C | normal | B | ambiguous-code | 5 | 0/0/5 | 0/0/5 | 0 | 0 | 10 | 10 |
| B/C | normal | C | ambiguous-code | 5 | 0/0/5 | 0/0/5 | 0 | 0 | 15 | 13 |
| B/C | normal | B | csv-sum | 5 | 5/0/0 | 5/0/0 | 0 | 0 | 6 | 6 |
| B/C | normal | C | csv-sum | 5 | 5/0/0 | 5/0/0 | 0 | 0 | 6 | 6 |
| B/C | normal | B | log-count | 5 | 5/0/0 | 5/0/0 | 0 | 0 | 5 | 5 |
| B/C | normal | C | log-count | 5 | 5/0/0 | 5/0/0 | 0 | 0 | 5 | 5 |
| B/C | normal | B | physical-lines | 5 | 0/5/0 | 0/5/0 | 0 | 0 | 5 | 5 |
| B/C | normal | C | physical-lines | 5 | 0/5/0 | 0/5/0 | 3 | 0 | 16 | 11 |
| B/C | minimal | B | ambiguous-code | 5 | 0/0/5 | 0/0/5 | 0 | 0 | 15 | 15 |
| B/C | minimal | C | ambiguous-code | 5 | 0/0/5 | 0/0/5 | 0 | 0 | 15 | 15 |
| B/C | minimal | B | csv-sum | 5 | 5/0/0 | 5/0/0 | 0 | 0 | 5 | 5 |
| B/C | minimal | C | csv-sum | 5 | 5/0/0 | 5/0/0 | 0 | 0 | 5 | 5 |
| B/C | minimal | B | log-count | 5 | 5/0/0 | 5/0/0 | 0 | 0 | 5 | 5 |
| B/C | minimal | C | log-count | 5 | 5/0/0 | 5/0/0 | 0 | 0 | 5 | 5 |
| B/C | minimal | B | physical-lines | 5 | 0/5/0 | 0/5/0 | 0 | 0 | 5 | 5 |
| B/C | minimal | C | physical-lines | 5 | 0/5/0 | 0/5/0 | 0 | 0 | 5 | 5 |

上表为完整 32 行（每格 5 次）；`正确`、`零审批` 为 true/false/null 计数，`n/a` 表示 A/A1 没有 P4 字段。

### 3.4 关键定性观察

1. **物理行数仍错误**：多数 normal/minimal 运行生成 `find . -type f -name '*.go' -print0 | xargs -0 wc -l`，返回 `{"files":3,"lines":5}`。`nested/space name.go` 以 CRLF 结束但没有末尾换行，`wc -l` 只数换行符，故 6 行被算成 5 行。该命令被分析器证明为只读并直接执行，没有审批；错误属于生成语义/口径，不是多余的审批。
2. **含糊代码统计没有改进**：模型常把“代码行数”解释为所有文件行数（16），或生成 `wc -l` 后自报 5 行；它没有稳定声明文件范围、物理/非空口径，也常附加未请求的明细表。自动正确性保持 null。
3. **明确多维报表未满足**：`requested-breakdown` 首轮常生成 `find ... | wc -l` 或 `tail -n 5`，没有按用户要求给出总量/生产/测试/父目录分组。该场景没有自动 oracle，未计入完成表。
4. **C 没有触发机会**：全部 B/C 首轮和完成报告中 `organization_reasons` 为 0。实际复杂调用是：
   - `process_run sh -c 'for ...'`：literal argv 没有内部 AST/IR；P4 按设计不额外解析，因此不构成候选。
   - `shell_run 'find ... | awk ...'`：unknown 但没有嵌套控制流、两个可变局部状态、动态求值或多层内联宿主，不满足高置信门槛。
   - `find | wc -l`：已证明只读，按设计不自动重组。
   因此 C 的 0 次自动重组是“没有候选”，不是预算耗尽、来源排除或实现失败。P4 的保守边界与 `process_run` 降级行为一致。
5. **B/C 差异不是 C 的效果**：normal C 的 `physical-lines` 出现 3 次 `approval_unavailable`、42 次调用；B 为 0 次、26 次调用。C 没有任何自动重组，差异来自模型采样（C 的若干次生成了 `sh -c` 复杂命令后被普通审批阻断）。不能把这些差异归因于 `once`。
6. **A/A1 与 B/C 没有稳定差异**：首轮 ask 为 15–17/70，静态读取 normal 44–48/70、minimal 34–44/70；完成正确率均为 10/20，零审批均为 10/20。每组只有 5 次，差异不足以宣称改善或退化。

## 4. 验证

| 检查 | 结果 | 日志 |
| --- | --- | --- |
| `go build ./...` | 通过 | `verification/build.log` |
| `go test ./... -count=1` | 通过（根模块全部包） | `verification/go-test.log` |
| `go test -race ./internal/app ./internal/config ./internal/commandanalysis ./internal/evals -run 'Test(CommandOrganization\|CommandSimplification\|CommandStructureP4\|ReorganizationMetrics)'` | 通过 | `verification/race.log` |
| `go test -tags commandtrace ./internal/app -run '^TestCommandAnalysisWorkBaseline$'` | 通过，0.023s | `verification/commandtrace.log` |
| `gofmt -l`（本轮 Go 文件） | 无输出 | `verification/gofmt.log` |
| `git diff --check` | 通过 | `verification/diff-check.log` |
| P5 策略 manifest + C 接线测试 | 通过 | `verification/p5-policy-wiring.log` |
| A/A1 normal 首轮/完成 | 测试流程 PASS；148.617s / 87.963s | `raw/a1/normal/*.log` |
| A/A1 minimal 首轮/完成 | 测试流程 PASS；133.544s / 95.639s | `raw/a1/minimal/*.log` |
| B/C normal 首轮/完成 | 测试流程 PASS；148.352s / 98.553s | `raw/bc/normal/*.log` |
| B/C minimal 首轮/完成 | 测试流程 PASS；134.967s / 102.093s | `raw/bc/minimal/*.log` |

`go run github.com/go-task/task/v3/cmd/task@v3.51.1 check` 在本环境未能启动：Task 模块拉取的 `google.golang.org/grpc@v1.79.3` 在 Go 1.27.3 下编译失败（`http2.TrailerPrefix` undefined）。改用可写临时模块缓存和 `GOTOOLCHAIN=go1.26.3` 重试时仅打印下载提示、长时间无进展后取消；更早的尝试因模块缓存只读无法写入 toolchain。Taskfile 的 `check`/`test` 实际命令分别是 `go build ./...` 和 `go test ./...`，两者已直接通过；不能把包装器失败写成“Task 通过”，也不能把直接命令通过写成包装器已验证。说明见 [`verification/task-wrapper.md`](verification/task-wrapper.md)。

## 5. 未验证与限制

- **平台**：只有 Linux amd64；没有真实 Windows PS5.1/PS7、macOS、Linux `pwsh`。P4 的 PowerShell 候选更适合在 Windows 上观察，本轮的 0 次自动重组不能外推到 Windows。
- **人工审批/UI**：非交互环境无法显示真实审批页；`actual_review_prompts` 固定为 0，`approval_unavailable` 只是“预计需要审批”的生产 caller 结果，不等于用户实际看过审批。没有人工 S/N/Ctrl+C、分页、窄终端、脚本变化复核或理解成本评分。
- **执行失败口径**：`allow_path_errors`/`tool_errors` 是生产 caller 的观察值，不是可信 dispatch 事件；本轮最终 B/C 数据均为 0，不能据此宣称“没有任何执行失败”。没有逐调用 dispatch 计数。
- **never 对照**：未运行；它会执行模型生成的未知命令，超出评测安全边界。P4 确定性测试覆盖 `never` 的零自动重组和显式关闭审查语义。
- **统计**：每格 5 次，仅作初步观察；没有置信区间或显著性声明。B/C 的随机差异不应被解释为效果。
- **真实用户文件/秘密**：全部使用合成夹具；没有把 provider key、真实用户文件或模型未知脚本写入证据。完成实验的只读调用在临时合成目录执行。
- **默认值**：未修改 `command-simplification` 默认值；未把 `once` 设为默认，也没有调整阈值来制造触发或收益。

## 6. 发布决策

C 相对 B 没有减少必要审批或理解成本的可观察证据；自动重组在 C 的 40 次完成会话（180 次总运行）中一次未触发；物理行数错误由只读命令的语义错误造成，`once` 按设计不会处理已证明只读调用。因此：

- 保持 `command-simplification: off` 作为默认值。
- 保留 `once` 作为显式 opt-in，不删除机制；P4 的请求级预算、来源排除和 typed `not_executed` 行为由单元/接线测试继续保护。
- 不因为本轮 P5 结果修改提示词或放宽 P4 候选门槛；若未来要解决物理行数，应单独设计生成质量改进并用新的固定模型对照验证，而不是让自动重组承担正确性职责。
- 要改变默认值，仍需 Windows/PowerShell 真实生成、人工 UI 验收和更大样本；本记录不足以支持该变更。

## 7. 文件索引

| 文件 | 内容 |
| --- | --- |
| [`environment.json`](environment.json) | 平台、Go、模型、参数、提交、策略指纹和运行规模 |
| [`summary.json`](summary.json) | 按组/模式/阶段/场景聚合的机器可读结果 |
| [`tables.md`](tables.md) | README 表格的完整生成结果（含 32 行按场景表） |
| [`summarize.py`](summarize.py) | 从原始 JSON 生成 summary/tables 的证据脚本 |
| `raw/a1/{normal,minimal}/` | A/A1 首轮/完成原始报告与日志 |
| `raw/bc/{normal,minimal}/` | B/C 首轮/完成原始报告与日志 |
| `verification/` | 构建、测试、race、commandtrace、P5 策略/接线日志及 Task 包装器说明 |

相关实现：`internal/app/command_generation_p5_integration_test.go`、`internal/app/command_generation_integration_test.go`（观测字段）、`internal/evals/command_generation.go`（`allow_path_errors`）、`internal/app/testdata/command_generation/p5_policy.json`。P4 机制本身见 [P4 记录](../2026-10-27-command-generation-p4/README.md)。
