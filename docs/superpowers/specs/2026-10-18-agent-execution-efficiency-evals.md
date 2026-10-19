# 小范围修改的执行评测

此评测区分最终文件正确性、可机械统计的会话指标和需要人工判断的执行质量。离线测试通过不意味着模型表现改善，也不意味着真实 Qt 窗口已生效。

## 场景与入口

`tests/blackbox/editing_efficiency_test.go` 与 `prompt_workflow_test.go` 复用真实 provider harness，在独立临时 HOME、配置、会话库和工作目录运行有明确目标的任务。澄清场景使用 `internal/app/clarification_integration_test.go` 的真实模型首轮评测，不执行模型请求的任何工具：

| case | 情景 | 自动检查 |
| --- | --- | --- |
| clear-modification | 明确的字号修改 | 无提问工具调用；最终正文基准和标题顺序正确 |
| missing-modification-context | 未指定两个窗口中的哪一个 | 先澄清；两个文件均未修改 |
| edit-recovery | 用户提供的旧片段与文件不同 | 最终文件正确；过期片段仅为线索，不强制模型先犯一次编辑错误 |
| check-recovery | 已存在 H3/H4 大小倒挂的部分修改 | 独立检查最终文件；是否先运行失败检查、如何恢复需人工复核 |
| visual-verification | 用户要求有兼容渲染器时截图 | 最终文件正确；截图真实性及不可用说明需人工复核 |
| simple-aggregation | CSV 列的简单汇总 | 总数正确、输入和工作目录不被修改；脚本选择与调用数供复核 |
| ambiguous-symptom-first-turn | 未说明屏幕或预期的字体异常 | 先用文字澄清，不调用工具检查 |
| installed-skill | 用户明确指定已安装的夹具技能 | normal 模式加载技能，并读取夹具中的标识 |

```sh
go test ./internal/evals -run 'TestEditFixture|TestExecution|TestMetrics' -count=1
go test -tags integration ./tests/blackbox -run 'TestRealAIBlackBox(SmallEdit|SimpleAggregation|InstalledSkill)' -count=5 -v -timeout=45m
go test -tags integration ./internal/app -run TestDeepSeekClarificationFirstRoundIntegration -count=5 -v -timeout=10m
```

真实评测需要现有 harness 支持的 provider key；用 `MODS_BLACKBOX_PROVIDER` 和 `MODS_BLACKBOX_MODEL` 固定 provider / model。缺少 key 会明确 SKIP。Node.js 用于执行合成 Markdown formatter 的有界结构检查；没有 Node.js 时字号夹具检查会跳过。

harness 默认使用 `minimal: false`。编辑和简单/复杂汇总分别运行 `normal` 与
`minimal` 子测试，后者是显式对照。用户技能场景只在 normal 模式运行。评分前
检查保存的 system 消息，确认 identity/workflow 的存在与所选模式一致。原始输出
使用 `raw: true`，因此终端提问工具不可用；真实交互允许/拒绝仍由分发测试覆盖。

缺失目标和模糊症状单独使用真实 provider 的首轮 normal/minimal 对照，固定
`temperature=0`、`max_tokens=1024`、关闭思考，并沿用生产提示词组装与工具定义。
不传入执行回调，也不调用 `CallTools`，从首轮 assistant 消息评分；任何检查请求
直接记为失败，不读取主机文件。这个用例只测是否先提问，不测工具执行。

HOME、USERPROFILE、技能目录、配置和会话库全部指向隔离夹具。每次进程记录
provider、实际模型、minimal、review mode、耗时及服务端返回的 token usage；
服务端不提供 usage 时保留 unavailable，不从字节数估算 token。没有 API key 时，
`TestHarnessPromptModes` 仍会验证生成的配置和目录隔离。

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
`normal` / `minimal` × `review-mode=auto` / `never` 四种组合分别运行。
auto 保留真实分类和审批：
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

先固定同一模型、采样参数、工具集和 fixture；在两个代码版本中各运行至少五次。每次记录提交、模型标识、配置、Go/Node 版本，以及 `execution report` JSON 和最终文件检查结果。normal 是提示词行为的主组，minimal 是显式对照；必须在同一 prompt mode 和 review mode 内比较版本。仅运行 minimal 不能验证 identity/workflow 的修改。交互审批通过独立 conformance/entrypoint 测试验证，不从非交互 harness 推断。

先比较文件正确率，再比较询问数、失败结果、工具调用数中位数和范围。不能用更少调用抵消错误结果，也不将调用预算变成运行时拦截。工具种类或模型不同的运行不直接归因于提示修改。

人工复核：是否提出无必要问题；未修改时是否无依据地猜测权限；是否检查层级关系；相关测试通过后是否仍无目的追加验证；是否准确区分文件变化、模拟检查和实际可见效果；用户要求的截图是否确实产生且对应目标。

## 提示材料体积回归

```sh
go test ./internal/app -run TestAssembledPromptBudgets -count=1 -v
go test ./internal/proto -run TestMeasurePromptSize -count=1
```

`TestFixedPromptFragmentsStayCompact` 仅保留固定文案的局部上限。
`TestAssembledPromptBudgets` 使用实际 system 组装、provider 能力分流和工具注册路径，
覆盖基础、文件、shell、组合、交互、项目/角色/用户技能、minimal 和无工具 provider。
每个夹具分别限制渲染后 system、工具描述、schema JSON、完整工具定义 JSON 和总量，
并输出各 system section 和工具的分项。schema 属性里的说明及系统层级标题也计入，
把文案搬进工具定义不能绕过完整输入预算。

总量是 system 文本加完整工具定义 JSON；描述与 schema 是分项，不能重复相加。
这是 provider-neutral 的字节度量，排除用户消息、历史与媒体，不是 token 或最终
provider wire 大小。预算只用于固定测试夹具，不限制用户项目指令、自定义技能或
MCP。调试模式的 `prompt bytes` 使用同一度量。

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


### 2026-10-19 提示词精简后的真实验证

环境：Linux amd64，Go 1.27.2，DeepSeek `deepseek-v4-flash`。
基于 `d4732b7` 加当前未提交修改；测试及运行时代码变更内容指纹 SHA-256 为
`ab3acb5b9bc591b9f148b2ad337654dc1c46c67c29045968134f21bcf30c50da`（不包含文档与日志）。
密钥仅在本次评测进程环境中使用，不写入仓库或夹具配置。

先运行了 19 个 CLI 场景的 smoke test：16 通过、3 失败。失败包括普通模式
先列目录后询问修改目标，以及两种模式在模糊症状下开始检查。旧模糊用例并未
构成文件系统沙箱，普通模式执行了 52 次调用并读取了夹具外的配置与图片；该
执行方式已移除，匹配内容的临时探测文件已清理。模糊症状与缺失目标现使用
不执行工具的首轮模型评测。其校准批次发现 thinking 设置和输出预算未对齐
生产调用，以及“问题后附说明句”被旧问句启发式误判；修正后重新运行，校准
批次不纳入下表。

identity 同时明确了“目标和预期清楚时直接执行；信息不足时在检查假定目标前
先询问”。这没有改变审批规则。下表为修正后的固定版本，每个组合独立运行
5 次。CLI 使用 provider 默认采样参数，澄清首轮固定 temperature=0、关闭思考、
max_tokens=1024；不同执行方式之间不直接比较耗时和 token。

```sh
go test -tags integration ./tests/blackbox -run 'TestRealAIBlackBox(SimpleAggregation|InstalledSkill|CodeStatistics|SmallEdit)/(normal|minimal)/(auto|never|clear-modification)$' -count=5 -v -timeout=45m
go test -tags integration ./internal/app -run TestDeepSeekClarificationFirstRoundIntegration -count=5 -v -timeout=10m
```

| 场景/模式 | 通过 | 工具调用中位数（范围） | 耗时中位数 / 秒 | 累计输入 token 中位数 |
| --- | --- | --- | --- | --- |
| CodeStatistics/normal/auto | 5/5 | 3（3～4） | 4.58 | 18283 |
| CodeStatistics/normal/never | 4/5 | 3（2～5） | 4.68 | 24075 |
| CodeStatistics/minimal/auto | 4/5 | 5（3～6） | 6.42 | 26214 |
| CodeStatistics/minimal/never | 5/5 | 3（2～4） | 4.15 | 15307 |
| SmallEdit/normal/clear-modification | 5/5 | 7（6～9） | 7.94 | 31344 |
| SmallEdit/minimal/clear-modification | 5/5 | 8（7～9） | 6.93 | 38840 |
| SimpleAggregation/normal | 5/5 | 2（2～2） | 3.82 | 17003 |
| SimpleAggregation/minimal | 5/5 | 3（2～3） | 4.93 | 19087 |
| InstalledSkill | 5/5 | 2（2～3） | 2.44 | 14969 |
| Clarification/normal/ambiguous-symptom | 2/5 | 1（0～1） | 0.98 | — |
| Clarification/normal/missing-edit-target | 5/5 | 0（0～0） | 0.8 | — |
| Clarification/minimal/ambiguous-symptom | 0/5 | 1（1～1） | 0.64 | — |
| Clarification/minimal/missing-edit-target | 5/5 | 0（0～0） | 0.66 | — |

澄清表中的工具调用仅为模型提出的请求，实际执行数始终为零；该批次的 usage
直接记在日志中，表中不填 CLI token 汇总。所有核心场景均无提问工具调用，也
没有复杂度简化重试。简单汇总使用直接管道，没有生成脚本。

结果没有全部通过，保留原始失败断言：normal/never 的复杂统计有一次总数正确，
但将生产代码 144 行、测试代码 42 行答成 158/28；minimal/auto 另一次将每个
文件多算一行，总数 198 而非 186。普通模式的模糊症状仅 2/5 先澄清，minimal
为 0/5，说明提示词仍不能稳定阻止先检查环境的倾向。新评测能暴露这些问题，
但不能据此宣称模型行为已全部修复，或将单次候选改动归因为确定的性能提升。

根模块构建、全量单元测试与字节预算检查通过。真实 CLI 和澄清评测均因上述
语义失败返回非零，不通过修改预算或放宽断言消除失败。
