# 命令分析重构交接记录

更新时间：2026-10-26（Windows 实机验收）。

## Windows 续作（优先于以下历史状态）

已同步周末提交至 `33dd565`，完成 Windows 全量测试及 PS7/PS5.1 真实桥验证，修复跨平台测试假设、旧授权断言及 Windows 验证脚本漏包/宿主隔离问题。新增经审查的 Windows 事实与 work 快照，旧快照未改。详情与剩余 UNC、Windows race、macOS 缺口见 [Windows 验收记录](docs/superpowers/plans/2026-10-26-command-analysis-windows.md)。

## 最新续作结果（优先于下方历史记录）

- 阶段 1 Linux 收尾已完成；用户随后明确授权实施阶段 2，并要求继续。
- 阶段 2 已实现共享解析、环境快照、固定类型查询/预算/取消、PowerShell IR v2 和取消零执行保护。详细证据见 [阶段 2 记录](docs/superpowers/plans/2026-10-24-command-analysis-stage2.md)。
- Linux 全量构建/测试、带计数测试、相关包 race 通过；Windows 根模块和 commandanalysis 测试交叉编译通过。真实 Windows host/快照/junction 验收仍待补，不能将交叉编译记作运行通过。
- 23 条 Linux 事实与阶段 1 完全相同；初次 POSIX 解析计数从 90 降为 17，复核从 60 降为 12。已加入审查后的 Linux 事实和 stage2 work 快照，原 Windows 快照未修改。
- 阶段 1/2 已提交为 `70b2737`，未推送。用户随后授权实施第三阶段。
- 阶段 3 已实现统一 catalog、选项绑定、注册校验、每次分析内的 invocation 事实复用、native/cmdlet/builtin 身份隔离；未审计规则保留 compatibility 标记。1,032 条独立旧实现观测通过差分检查，date/hostname 及 wrapper/身份的安全收紧有单独测试。详细实现与限制见 [阶段 3 记录](docs/superpowers/plans/2026-10-24-command-analysis-stage3.md)。
- 阶段 3 Linux 全量 build/test、commandtrace、相关 race 通过；Windows 测试和根模块交叉编译通过。原 app Linux/Windows 事实及 work 快照未修改；真实 Windows/macOS 执行仍待补。
- 第三阶段已提交为 `0ce4129`，未推送。用户随后授权实施第四阶段。
- 第四阶段已接入角色化目标、操作相关叶子/父目录解析、唯一 app 授权转换及叶子绑定复核；`KnownDirs` 与候选路径不再授予写权限。真实审批链新增混合读写、读取来源动态变量、同目录链接变化及持续变化零执行测试。见 [阶段 4 记录](docs/superpowers/plans/2026-10-24-command-analysis-stage4.md)。
- 第四阶段 Linux 全量构建/测试、commandtrace、相关 race 已验证；Windows 根模块、commandanalysis/app 测试仅交叉编译，真实 Windows/macOS 尚待执行。23 条 Linux 样本审批结果不变，新增独立 stage4 事实快照；原平台事实/work 快照未改。
- 第五阶段首批已实现 uniq 参数、普通文件写严格选项、经文件类型证明的 cp/mv、Git 只读前序上下文及 wrapper 边界。详见 [阶段 5 记录](docs/superpowers/plans/2026-10-25-command-analysis-stage5.md)。23 条 Linux 固定样本有 1 条复制 Ask→Allow；移动补齐两端目标但因缺少源权限仍 Ask。原 safety 断言与旧快照未改。
- 第五阶段 Linux build/test、commandtrace、相关 race 通过；Windows 根模块及测试仅交叉编译。PowerShell 新参数/重定向扩展及真实 Windows/macOS 验收待补，阶段 6 未开始。
- 第四、五阶段已按用户要求合并到本次本地提交，未推送。不要重跑一次性迁移脚本；本次提交包含两个阶段的代码、测试、快照与文档。

- 用户随后要求继续第五阶段。本轮已补齐 touch 有值参数/短选项组合、受限 env 写入、Git 全局参数、PowerShell 标量内容写与 AST 重定向目标。新增 10 条固定样本相对 `54060c5` 有 5 条 Ask→Allow、其余负例继续 Ask；原 23 条事实及 work 快照不变。见 [后续实施记录](docs/superpowers/plans/2026-10-25-command-analysis-stage5-extension.md)。
- 本轮 Linux build/test、commandtrace、race 及 Windows 交叉编译通过；Linux PowerShell 7.6.8 的 15 个真实桥样本和 7 个隔离执行正例通过。真实 Windows/macOS、PowerShell 5.1 与 junction 验收仍待补，第六阶段未开始。
- 第五阶段后续实施时的基线为 `54060c5`；此前第四阶段与第五阶段首批已在该提交中。后续改动的提交状态见下方第六阶段记录。

- 用户要求实施第六阶段。本轮已删除 approval 命令薄包装与 KnownDirs 旧授权转换，迁移所有调用及测试，加入结构/依赖边界、组合性质与三组 fuzz，补冷/热基准和发布说明。见 [阶段 6 记录](docs/superpowers/plans/2026-10-25-command-analysis-stage6.md)。
- 第六阶段 Linux build/test、commandtrace、race 与约 26 万次 fuzz 通过；Linux PowerShell 真解析/隔离执行通过；Windows/macOS 根模块及 app/commandanalysis 测试交叉编译通过。真实 Windows/macOS 仍待验收，不能说全平台最终出口已完成。
- 用户要求提交代码：第五阶段后续与第六阶段改动合并到本次本地提交，包含兼容文件删除、新增测试、快照和文档；父提交为 `54060c5`，未推送。下一步仍需在真实 Windows/macOS 上执行平台矩阵。

以下第 0–10 节为 2026-10-23 历史交接记录，其中“下一台机器”待办及阶段状态已由上方结果更新。

## 0. 当前提交状态（本机交接时已提交）

- 阶段 0、阶段 1 已写代码、设计/计划/基线文档、AGENTS.md 增量说明以及本文件已作为一个 WIP 提交提交到本地 `main`，便于在另一台机器继续。
- 提交内容包含 tracked 修改、删除（Git 识别为 `R` 重命名）以及全部 untracked 新文件：`internal/commandanalysis`、`internal/commandtrace`、`internal/app/testdata/command_analysis` 快照、`internal/app/command_analysis.go`、`internal/approval/command_compat.go` 等。
- **未包含**根目录一次性迁移脚本 `.stage1_*.py`（有意留在原机器工作区，不进入历史）。
- 该提交按用户要求是“记录当前状态”，**没有**在提交前重新跑全量验证。
- 本次也没有执行 push。另一台机器拉取前请确认是否需要 `git pull`/`git push`，或直接使用本地提交所在分支（若未推送，需通过 origin push 或其它方式传递）。

继续工作前请勿 `git reset`/`git clean` 掉此提交，也不要重跑 `.stage1_*.py`。

## 1. 当前目标与授权范围

用户希望降低命令审批误报，已认可详细设计及分阶段计划，并明确要求实施阶段 1。

核心方向：

1. 独立 `internal/commandanalysis`，与应用编排、授权策略分开。
2. 后续建立统一命令语义库，一次返回 effect、输入/输出目标、覆盖完整性与缺口。
3. **暂不恢复 LLM 自动放行，也不恢复分类模型请求、分类缓存或模型建议路径。**
4. 阶段 1 只做模块迁移和契约建立，保持既有审批行为。重复解析、统一 catalog 和目标角色改造分别属于后续阶段。

最新请求是编写本交接文件，便于换机器继续。阶段 1 在收尾前被中断，不应标记为完成。

## 2. 工作树与跨机器携带要求

- 原工作目录：`C:\Users\panjie\dev\mods`。
- 分支：`main`。
- 当前 HEAD：`c502ea25125ae68888a9fa7b116d10657e76b65f`。
- **本轮阶段 0、设计文档及阶段 1 修改已提交（见第 0 节）；校验和之前的状态描述如下，供核对历史。** 提交前 HEAD 为 `c502ea25125ae68888a9fa7b116d10657e76b65f`；本文件所在提交即包含全部阶段 0/1 内容的 WIP 提交。
- `git status` 中大量旧文件为删除、新目录为 untracked，这是搬迁尚未暂存时的正常状态。不能只携带删除记录。
- **普通 `git diff` 不包含 untracked 新文件。** 这些文件现已随 WIP 提交进入历史；若另起分支或 cherry-pick，请确认 `internal/commandanalysis`、基线 JSON、新适配文件与文档都在提交内（提交前已用 `git status` 核对，重命名均显示为 `R`）。
- 所有新文件保持 UTF-8、LF。
- 阶段 0 之前已有的修改和文档同样应保留，不要用 reset/clean 清除。

仓库仍有临时迁移脚本，见第 8 节。它们不是产品代码，未随本次提交进入历史，收尾前需要在原机器删除。

## 3. 先阅读的文档

1. [设计](docs/superpowers/specs/2026-10-23-command-analysis-catalog-design.md)。
2. [分阶段实施计划](docs/superpowers/plans/2026-10-23-command-analysis-catalog.md)。
3. [阶段 0 基线报告](docs/superpowers/plans/2026-10-23-command-analysis-baseline.md)。
4. [上一次审批核心重构设计](docs/superpowers/specs/2026-10-22-command-approval-refactor-design.md)。
5. 根目录 `AGENTS.md`，尤其授权完整性、真实路径、prepared call、禁止命令名前缀授权等约束。

注意：设计和计划的状态文字目前仍写阶段 1 待实施，AGENTS.md 的主体架构说明也仍包含旧路径。代码已迁移大部分，但这些现状文档尚未同步收尾。不能把旧状态文字当成“阶段 1 尚未动代码”。

## 4. 阶段 0：已经完成

### 4.1 可回放基线

- `internal/app/command_baseline_test.go`。
- `internal/app/testdata/command_analysis/cases.json`。
- 同目录 `baseline_windows.json`、`work_windows.json`。
- `internal/commandtrace/{trace,enabled,disabled,trace_test}.go`。

31 个样本：POSIX 17、literal argv 6、PowerShell 8。分类为读取 7、已授权/安全临时写 3、安全反例 18、已知限制 3。

人工安全断言与兼容事实快照分开。安全反例必须 ask，读取及现有安全写快速路径继续 allow。Git 多 push URL 另有必须保留全部 origin 的断言。

`-update-command-baseline` 只能记录观测，不修改人工安全断言；断言失败时拒绝更新事实快照。**迁移时不要批量重录快照来消除差异。**

`commandtrace` build tag 启用无内容计数，默认构建为空操作。记录 POSIX parser、bridge 入口/round trip、固定 Git 查询次数。计数不参加授权，也不保存命令或秘密。

fixture 中 Git 仅执行本地初始化、配置和固定读取查询，远端用 `.example`，从不真的 push；继承的 `GIT_*`、系统及全局配置被隔离并在测试后恢复。

### 4.2 已确认的当前限制

- `cp` 能提取目的地，但 coverage incomplete，不能复用仅覆盖输出的授权。
- `mv` 当前主要记录目的地；依靠 incomplete 阻止只批准目的地后放行。将来补 complete 前必须补源目录项修改。
- PowerShell 文件重定向的操作/目标绑定不足，仍可能 unknown/incomplete。
- Git 远端分析将前面的非 Git 调用视为上下文变化；即使前面是只读也会影响远端解析。
- Windows 上显式 POSIX 测试入口中，主分析按传入 flavor，但 Git 辅助解析按工具名/GOOS 选择 PowerShell。这是后续要修复的方言漂移。
- 原计数覆盖了 approval 中五处 POSIX parser 调用，**不代表完整 profiler**；原 app 的 `commandMutatesPOSIXEnvironment` 也会解析，但未加计数点。阶段 2 完善观测时应明确解释计数变化。

基线中常见现状：PowerShell 普通调用解析两次；Windows 显式 POSIX 样本记录 4–5 次 POSIX parse，另有一次 bridge 调用。写调用复核再次重复分析。

这些是构造样本与实现现状，不是线上误报率数据。

## 5. 阶段 1：目前已经修改的代码

### 5.1 新分析模块

`internal/commandanalysis` 当前有 61 个文件（包含测试和嵌入 PowerShell 脚本）。主要内容：

- 从 approval 搬入 assessment、shell_static、readonly、readonly_ps、readonly_policy、coverage、writable_dirs。
- 搬入 shell_parse、invocations、external_paths、posix_runtime_targets、scalar_subst、paths。
- 搬入 git_static、ps_bridge、ps_bridge.ps1、ps_probe、ps_literal。
- 搬入 command_constraint、command_families、reviewability、script_file。
- 原 `approval/remote.go` 中纯 origin 规范化也搬入新包；approval 通过兼容层委托，避免分析器反向依赖策略。
- 原 app 的环境/字面目标算法搬入 `stable_env.go`、`posix_env.go`、`literal_target.go`。
- 原 app 的分析编排和目标合并搬入 `pipeline.go`。
- 原 app 的 Git/字面远端分析搬入 `resolve_git.go`。

新增的模块契约：

| 文件 | 当前实现 |
| --- | --- |
| `input.go` | tagged Input，恰有 Shell 或 Process；显式方言、cwd、argv、遮蔽环境名称；复制输入可变数据 |
| `dependencies.go` | Settings 与宿主事实；传入工作区、显式只读命令和不可信程序目录；没有审批规则 |
| `engine.go` | `NewEngine`、`Analyze`、程序绑定约束；暂留 focused Git discovery 接口 |
| `result.go` | `Result = CommandAssessment` 的过渡模型及深复制 |
| `engine_test.go` | 输入错误/取消、信任配置快照、输入隔离、结果深复制、禁止反向导入 |

当前 `Dependencies` 只包含 `UntrustedProgramDirs`。文件系统、环境、Git 查询依然使用搬来的宿主实现；窄查询接口、总预算和完整上下文快照留到阶段 2。

`ShellInput.InvocationDialect` 是明确标注的过渡字段：保留原 Git 辅助解析方言，保证阶段 1 不顺带改变基线。阶段 2 使用同一 ParsedCommand 后应移除。

`Result` 仍保留 KnownDirs 等 legacy 字段；还没有角色化 Target，也没有统一 catalog。不能据此提前宣称阶段 3/4 已完成。

### 5.2 app 薄适配层

新增 `internal/app/command_analysis.go`：

- 选择 shell 方言。
- 从 Config 构造独立 Settings，把安全目录作为“不可信程序来源目录”事实传给模块；不向模块传入 saved rules。
- 解码 process JSON、规范实际 cwd，只传 secret_env 的名称。
- 调用 Engine.Analyze，适配回当前审批事实类型。
- 将已解析 process binding 交给模块检查。
- `commandResultToIntent` 为初始审批与重新评估共享的转换入口；内部暂时委托 legacy AccessIntent 映射。

最近一批改动已使：

- `app/review.go` 初始 intent 构造使用 `commandResultToIntent`。
- `app/request_session.go` 复核转换也使用同一个函数。
- 两个文件已增加 `commandanalysis` import 并运行 gofmt。

原 `app/shell_assess.go` 已删除，其方法入口迁入新适配文件，算法迁入模块。

`app/remote_targets.go` 现在只保留 URL 显示脱敏，原分析部分已搬出。

`app/command_analysis_compat_test.go` 中保留 focused Git discovery 的测试包装，只有测试编译会使用；生产调用走 Analyze。

### 5.3 approval 和 CLI

- 新增 `internal/approval/command_compat.go`：旧公开类型、常量、函数的过渡门面；分析全部委托新模块。
- `CommandAssessment` 是基于模块类型的命名兼容类型；保留 `AccessIntent()`、`AddGap()`、结构诊断委托。
- evidence/coverage/gap 等当前是过渡别名，并非最终分层映射。
- `approval/facts.go` 只保留 AccessIntent 的完整写资格检查。
- access、matching、rules、safe_dirs 的授权策略仍在 approval。
- `internal/cli/main.go` 改为直接调用 `commandanalysis.CloseBridge()`，维护 bridge 生命周期。
- 旧兼容门面尚未删除，按计划后续逐步清理；不要重新引入第二套分析实现。

### 5.4 测试迁移

- approval 的纯分析/解析/bridge 测试随实现搬到新包。
- 原 app 的纯环境、字面目标、probe 物化测试搬到新包。
- 从混合 app 测试文件抽取纯 helper 测试，放到 `pipeline_helpers_test.go` 和 Windows 对应文件。
- app 继续保留审批、prepared access、dispatch、完整流水线和真实目标变化等集成测试。
- `approval/command_intent_test.go` 保留策略转换测试；纯分析测试中的部分 AccessIntent 断言改成等价的 effect/dynamic fact 断言。
- 用临时审计脚本比较 HEAD 的 app/approval 命名测试与当前三个包：**514 个原有命名测试均能找到，没有缺失测试名称**。这只证明测试名保留，不能代替代码评审和断言等价性检查。

## 6. 测试结果与最终状态的区别

本机：Windows amd64，Go `go1.26.3`，Git `2.55.0.vfs.0.10`，实际 bridge 为 pwsh `7.4.14`。

阶段 0 已完成构建、根模块测试、独立回放和 cold/warm 基准；详情见基线报告。

阶段 1 过程中已确认通过：

```text
go test ./internal/commandanalysis ./internal/approval ./internal/app -run '^$'
go test -tags commandtrace ./internal/app -run '^TestCommandAnalysis(Baseline|WorkBaseline)$' -count=1
go run github.com/go-task/task/v3/cmd/task@v3.51.1 check
go run github.com/go-task/task/v3/cmd/task@v3.51.1 test
go test ./internal/commandanalysis -run 'Test(Engine|ResultClone|AnalysisPackage)' -count=1
git diff --check
```

重要时序：

- 模块主要搬迁完成后，31 个事实基线和 work 计数快照通过，**未重录 JSON**。
- 主要搬迁后的 check 与根模块 test 全部通过，包括 commandanalysis、approval、app、cli、tools 等。
- **随后**补了 `commandResultToIntent` 对初始审批/复核的统一接入及相关 import，并整理少量注释/import。
- 最新 focused Engine/Clone/依赖边界测试已完成：`ok .../internal/commandanalysis 3.740s`。中断后本次交接已取回退出码 0。
- 最新工作树 `git diff --check` 也已通过。
- **最新适配器接入后的最终全量 check/test 和带计数回放尚未重新执行。** 下一台机器需补此轮最终验证，不可仅引用之前通过结果。
- 交接提交本身未运行任何测试（用户明确要求只提交现状）。上面的结论仍只覆盖提交前的代码，不能视为交接提交已验证。

没有执行 Linux/macOS 实际测试、race、PowerShell 5.1 实际 host 验证；没有触发 provider 集成、UAC 或远端推送。

## 7. 下一台机器建议继续顺序

1. 确认拉取/携带到了包含阶段 0/1 的 WIP 提交（第 0 节），再读本文件与 AGENTS.md；工作区应只剩 `.stage1_*.py` 之类的未跟踪脚本。
2. 检查迁移差异，尤其 app 薄层与 module 的职责边界、测试断言迁移、Clone、输入错误/取消处理、旧门面委托。
3. 重点审查 app 对 Analyze error 的处理：目前包装为 UnknownCommandAssessment，以保持旧 malformed-input 的保守路径；取消/内部错误是否应显式向调用层传播，需要结合阶段 1 行为要求审查，不能静默自动放行。
4. 运行以下最终验证，**不要先重录基线**：

```text
go test ./internal/commandanalysis ./internal/approval ./internal/app ./internal/cli -count=1
go test -tags commandtrace ./internal/app ./internal/commandtrace -run '^(TestCommandAnalysis(Baseline|WorkBaseline)|TestCountersAreOptIn)$' -count=1
go run github.com/go-task/task/v3/cmd/task@v3.51.1 check
go run github.com/go-task/task/v3/cmd/task@v3.51.1 test
git diff --check
```

5. Windows 上核实真实 bridge、CLI CloseBridge 生命周期、junction/target reapproval 测试正常。其它平台没有现成事实快照时只运行适用语义断言，不能写成事实等价已验证。
6. 整理模块内搬迁遗留的注释，例如部分仍写“approval 的分析”或旧文件路径；不要改行为来顺带修复后续阶段问题。
7. 更新 AGENTS.md：shell classification 路径、app map、分析测试路径、bridge 生命周期。不要把语义库或角色化目标写成已经实现。
8. 更新设计/计划的阶段状态，写阶段 1 实施记录与测试证据，列出后续兼容层删除清单。只有最终验证和收尾完成后才将阶段 1 标为完成。
9. 清理本轮临时 `.stage1_*.py`；文件清单见下一节。保留真正产品代码、测试与基线。
10. 阶段 1 完成后，若继续后续授权任务，从阶段 2 的共享 ParsedCommand、上下文和查询接口开始。当前用户最新请求只要求交接文件，不在本次交接操作中继续实施阶段 2。

## 8. 临时脚本：不要重新跑迁移

根目录存在：

```text
.stage1_migrate.py
.stage1_continue.py
.stage1_app.py
.stage1_tests.py
.stage1_windows_tests.py
.stage1_audit.py
```

前五个脚本是本次一次性拆文件/搬迁工具，**非幂等，原文件已搬走，不要重新执行**。它们也不是代码生成的长期来源。

`.stage1_audit.py` 只读对比 HEAD 中旧测试名和当前测试名，已输出 `Original named tests: 514 Missing after migration: []`；可参考，但不是必需发布资产。

初次迁移脚本曾因 Windows 默认编码和函数签名正则失败；后续续迁脚本修复、编译和全量测试已经通过。当前不要从第一个迁移脚本重来。

清理时使用明确的文件路径，不做递归删除，不清理其它未提交文件。

注意：本次交接提交**未包含**这些脚本，因此换机器后工作区里不会出现它们，也不需要在新机器上清理；如需删除请在原机器按上述路径执行。

## 9. 后续阶段仍需保留的约束

- effect 与完整目标覆盖独立。找到部分目录/远端不等于完整。
- 未知节点不能被其它已知目标、temp、保存规则掩盖。
- cwd 是执行上下文，不是写授权。
- 读取源与写目标的区分要在后续语义模型中完成；目前 KnownDirs 仍是兼容字段。
- 目录/origin 规则只在完整范围下复用；provider/未知范围继续单次审批。
- 不添加命令名、shell 前缀或任意脚本位置的授权匹配。
- 不改写用户命令、不自动重放工具；保持 prepared call、最多一次重新准备/审批和持续变化失败关闭。
- 不恢复 LLM 自动放行。

## 10. 可粘贴给下一台机器的继续提示

> 请先阅读根目录 Handsoff.md、AGENTS.md 和 2026-10-23 命令分析设计/计划。阶段 0 已完成；阶段 1 的主要模块迁移已在交接 WIP 提交中（未验证过全量测试），最终适配器接入后还需重新全量验证、代码审查和同步文档。请从该提交继续，不重跑 .stage1 迁移脚本（也未随提交携带），不重录基线掩盖差异。保持审批行为，暂不恢复 LLM 自动放行。先完成阶段 1 收尾与相应测试，并准确报告平台验证范围。
