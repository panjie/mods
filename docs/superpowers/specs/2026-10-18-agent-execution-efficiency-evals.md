# 小范围修改的执行评测

此评测区分最终文件正确性、可机械统计的会话指标和需要人工判断的执行质量。离线测试通过不意味着模型表现改善，也不意味着真实 Qt 窗口已生效。

## 场景与入口

`tests/blackbox/editing_efficiency_test.go` 复用现有真实 provider harness，在独立临时 HOME、配置、会话库和工作目录运行：

| case | 情景 | 自动检查 |
| --- | --- | --- |
| clear-modification | 明确的字号修改 | 无提问工具调用；最终正文基准和标题顺序正确 |
| missing-modification-context | 未指定两个窗口中的哪一个 | 先澄清；两个文件均未修改 |
| edit-recovery | 用户提供的旧片段与文件不同 | 最终文件正确；过期片段仅为线索，不强制模型先犯一次编辑错误 |
| check-recovery | 已存在 H3/H4 大小倒挂的部分修改 | 独立检查最终文件；是否先运行失败检查、如何恢复需人工复核 |
| visual-verification | 用户要求有兼容渲染器时截图 | 最终文件正确；截图真实性及不可用说明需人工复核 |

```sh
go test ./internal/evals -run 'TestEditFixture|TestExecution|TestMetrics' -count=1
go test -tags integration ./tests/blackbox -run 'TestRealAIBlackBoxSmallEdit|TestRealAIBlackBoxMissingEditTarget' -count=5 -v -timeout=45m
```

真实评测需要现有 harness 支持的 provider key；用 `MODS_BLACKBOX_PROVIDER` 和 `MODS_BLACKBOX_MODEL` 固定 provider / model。缺少 key 会明确 SKIP。Node.js 用于执行合成 Markdown formatter 的有界结构检查；没有 Node.js 时字号夹具检查会跳过。

`testdata/editing/check.mjs` 检查正文使用 body、H1–H3 确实变小、H1–H6 顺序不倒挂，覆盖四种正文基准。测试使用可信原始检查器独立校验模型修改后的文件；模型修改检查脚本不能制造通过结果。它不是 QML 渲染器，不测量真实字体，也不证明截图或桌面重载生效。

## 保存会话评分

```sh
go run ./internal/evals/cmd/score -db /path/to/mods.db -session SESSION_PREFIX -case clear-modification -json
```

评分 CLI 的 `-case` 帮助自动列出所有案例。`-strict` 只根据声明的机械维度返回退出状态；整体 PASS 不代表任务语义正确。新场景会在 notes 中明确要求独立检查结果和人工复核。

统计项：

- `tool_calls`：只计算 assistant 的工具请求，不重复统计 tool 结果携带的调用元数据。
- `user_input_calls`：包括非法或取消的提问请求；不能仅凭次数断言提问是否必要。
- `failed_tool_results`、`failed_user_input_results`：依赖结果的 IsError 标志；后者可能是取消、交互不可用或参数错误，不能全部称为非法参数。
- `simplification_suggestions`：失败结果以 mods 的简化建议前缀开头的次数，不等于最终拒绝执行。
- `final_answer_characters`：最后一条没有工具请求的 assistant 文本的 Unicode 字符数，不是 token 数。

历史会话没有错误标记或完整结果时，失败计数可能不完整。当前消息存储不提供可靠的逐调用耗时/usage，评分器不从文本估算。精确的非法参数分类同样需要结构化错误类别，本次不靠易误判的报错关键词推断。

## 代码量统计与审查开启场景（2026-10-19）

`TestRealAIBlackBoxCodeStatistics` 使用真实 provider 和生产工具分发入口，在
`review-mode=auto` 与 `never` 两种模式分别运行。前者保留真实分类和审批：
非交互 harness 遇到需要人工审批的未知行为会明确失败，不能静默降级成 never。
交互允许/拒绝、saved rules 和 always 模式由 `internal/app/command_dispatch_test.go`
的分发回归测试覆盖。

夹具包含 12 个已跟踪 Go 文件、带空格的路径、测试文件、三个目录，以及未跟踪的
Go 干扰文件和 Markdown 文件。任务明确统计物理行（含空行、注释）。期望值直接从
可信夹具计算，逐项检查总量、生产/测试分类、目录分组及原文件未修改。

```sh
go test -tags integration ./tests/blackbox -run TestRealAIBlackBoxCodeStatistics -count=5 -v -timeout=15m
go run ./internal/evals/cmd/score -db /path/to/mods.db -session SESSION_PREFIX -case code-statistics -json
```

`code-statistics` 的四次调用预算是离线观察目标，不限制实际执行，不用少调用抵消
错误数字。harness 记录每轮总耗时；不是单次工具耗时或 token 用量。做修改前后
比较时固定模型、参数和模式，各运行至少五次，先比较正确率，再比较调用数和耗时
的中位数与范围。auto/never 对照本身不能证明版本改动的因果效果。

2026-10-19 起复杂度不再返回失败工具结果。保留 `simplification_suggestions`
用于读取旧会话；新评测要求此值为零。旧 Starship 模拟评测中的装饰格式仅记录，
继续断言安装完成、配置写入、工具参数与无额外 execution-policy 修改。

## 修改前后对照方法

先固定同一模型、采样参数、工具集和 fixture；在两个代码版本中各运行至少五次。每次记录提交、模型标识、配置、Go/Node 版本，以及 `execution report` JSON 和最终文件检查结果。对照测试使用的 harness 配置为 minimal=true、review-mode=never，专门观察执行策略；命令建议和审批通过独立 conformance/entrypoint 测试验证，不从该 harness 推断。

先比较文件正确率，再比较询问数、失败结果、工具调用数中位数和范围。不能用更少调用抵消错误结果，也不将调用预算变成运行时拦截。工具种类或模型不同的运行不直接归因于提示修改。

人工复核：是否提出无必要问题；未修改时是否无依据地猜测权限；是否检查层级关系；相关测试通过后是否仍无目的追加验证；是否准确区分文件变化、模拟检查和实际可见效果；用户要求的截图是否确实产生且对应目标。

## 本次验证记录

离线评分器、夹具、工具契约、提示及 shell conformance 定向测试通过。新增真实 provider 场景因当前环境没有匹配 API key 而跳过，尚无修改前后模型表现数据。完整根模块构建/测试结果见实施记录；跨平台行为仍由现有 CI 验证。

### 2026-10-19 复杂度关卡移除的验证

环境为 macOS arm64、Go 1.26.6；真实评测固定为 DeepSeek `deepseek-v4-flash`。
`task check`、`task test`（根模块全量）通过，Windows amd64 的 app 测试交叉编译
通过；未在本机执行 Windows 测试。提示仍符合原有 10.25 KiB 长度上限。

两批均为本次修改中的候选版本，不是旧版与新版 A/B。第一批每种模式各五次：
auto 正确 5/5、调用 2～5 次；never 正确 2/5、调用 3～6 次。随后补充在工具中
完成汇总、避免心算的提示，第二批结果如下（末尾仅压缩了重复提示措辞）：

| 模式 | 正确结果 | 工具调用中位数（范围） | 总耗时中位数（范围） | 简化重试 |
| --- | --- | --- | --- | --- |
| auto | 4/5 | 2（2～7） | 5.09 秒（3.35～12.64） | 0 |
| never | 5/5 | 4（2～5） | 8.32 秒（4.57～9.24） | 0 |

第二批 auto 的错误是工具返回逐文件行数后，最终答案把生产代码 144 行写成了
145 行；独立检查据此失败，没有为了低调用数放宽断言。另一次 auto 有一个普通
工具失败结果，但最终统计正确。所有运行均无复杂度简化重试。这验证了重试路径
已移除，不能证明模型统计能力已稳定，或把模式间差异归因于审查策略。

旧 `TestDeepSeekCommandSimplicityIntegration` 的最后一次运行未在十轮工具调用内
结束，因此真实模型集成评测未全部通过。模拟 runtime_info 已与生产返回结构
对齐，命令检查区分了 `Get-Command winget` 查询与实际安装；没有提高轮数预算或
放松安装/配置完成要求来消除该失败。
