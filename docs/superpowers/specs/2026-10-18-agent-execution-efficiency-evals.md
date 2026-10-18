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

## 修改前后对照方法

先固定同一模型、采样参数、工具集和 fixture；在两个代码版本中各运行至少五次。每次记录提交、模型标识、配置、Go/Node 版本，以及 `execution report` JSON 和最终文件检查结果。对照测试使用的 harness 配置为 minimal=true、review-mode=never，专门观察执行策略；命令建议和审批通过独立 conformance/entrypoint 测试验证，不从该 harness 推断。

先比较文件正确率，再比较询问数、失败结果、工具调用数中位数和范围。不能用更少调用抵消错误结果，也不将调用预算变成运行时拦截。工具种类或模型不同的运行不直接归因于提示修改。

人工复核：是否提出无必要问题；未修改时是否无依据地猜测权限；是否检查层级关系；相关测试通过后是否仍无目的追加验证；是否准确区分文件变化、模拟检查和实际可见效果；用户要求的截图是否确实产生且对应目标。

## 本次验证记录

离线评分器、夹具、工具契约、提示及 shell conformance 定向测试通过。新增真实 provider 场景因当前环境没有匹配 API key 而跳过，尚无修改前后模型表现数据。完整根模块构建/测试结果见实施记录；跨平台行为仍由现有 CI 验证。
