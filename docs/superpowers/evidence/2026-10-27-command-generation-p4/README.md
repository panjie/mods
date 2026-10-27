# P4 请求级一次自动重新组织

日期：2026-10-27。基线提交：`952d02a`。在现有未提交 P4 改动上完成实施、审查与回归；P5 真实生成/人工交互评测尚未开展。

## 行为

- 新增持久配置 `command-simplification: off | once`，默认 `off`，环境变量 `MODS_COMMAND_SIMPLIFICATION`；没有同名 CLI flag。该设置只控制自动组织反馈，不改变 `review-mode` 或授权。
- 启用时，新用户请求创建独立内存状态；默认 off 不创建组织状态。工具轮次、provider retry、checkpoint 恢复及 registry 重建复用同一状态；一次预算通过互斥区单调消费，完成/取消时关闭。回调捕获原请求上下文，旧 runner 的完成事件不能关闭新请求。人工 S 记录独立，不消费或返还自动预算；相同载荷不再自动反馈，记录有界。
- 候选仅限内置命令工具，分析成功且具有 AST/IR 结构事实；整体 unknown，或已知写入混有明确的未建模效果，才考虑自动组织。单纯写目标覆盖不完整不触发。还必须具有以下至少一种结构：未建模控制流深度至少 2；未建模控制流与至少两个不同局部赋值目标；动态求值；AST/IR 中存在至少两层内联宿主。多个独立宿主和单个 `sh -c` 不足以触发。
- 复用现有解析与语义结果获取这些事实，没有增加主解析、规则评估或文件读取。POSIX 排除命令前环境绑定，PowerShell 对普通 `$name` 去前缀并忽略大小写；不以 assignment 总数冒充不同状态变量。字面载荷片段仅供来源排除，禁止进入 JSON/日志，Clone 深拷贝集合。
- 来源检查包含 CLI prefix、当前输入、历史用户消息及本轮 `request_user_input` 答案。完整命令、保守空白归一化匹配或相同字面内联片段来自用户时排除；当前输入缺失、含代码围栏或来源处理失败也保守排除。现有脚本、动态/无扩展名文件宿主及未绑定包装器不自动改写。来源判断不是权限证据。
- `process_run` 只有 literal argv 事实而无内部代码结构时继续普通审批；不增加一次内部语言解析来制造候选。MCP、已免审调用、显式读取信任和 `ReviewNever` 不触发。normal/minimal 共用门槛；非交互可收到一次反馈，之后仍需审批则返回原有 unavailable。
- 自动反馈复用 P3 的 `mods.command_reorganization` / `not_executed` 返回；保留 provider call ID 并保证原调用不 dispatch。自动机会耗尽后，后续调用照常进入现有授权路径。任何替代调用重新分析、重新审阅。
- 未引入命令改写、执行重放、LLM 分类器、分析器授权事实或保存规则。
- `internal/evals` 保留历史 `failed_tool_results` 和建议计数；新增自动/人工重新组织与真实执行失败计数入口。新计数使用本地类型事件和显式 dispatch 记录；只有文本历史时执行失败等值保留 unavailable，不把伪造控制 JSON 当作简化事件。

## 变更位置

- `internal/app/command_organization.go`：请求状态、候选门槛和原子消费。
- `internal/app/completion.go`、`mods.go`、`request_session.go`：请求生命周期及授权前控制结果接线。
- `internal/app/review.go`：查询当前策略是否确实会进入人工审批，不启动第二份审批流程。
- `internal/commandanalysis/structure.go`、`result.go`：语法事实和 Clone；不更改效果、目标、coverage 或目录权限。
- `internal/config/config.go`、`save.go`、`config_template.yml`：配置、默认值、Help 和严格写入校验。
- `AGENTS.md`、设计/计划文档、`CHANGELOG.md`：更新政策状态和发布说明。

## 验证

环境：Linux amd64，Go 1.27.3，CGO_ENABLED=1，有 gcc；PATH 中没有 PowerShell。本轮没有执行模型生成的未知命令：分发断言均使用模拟工具，真实解析仅处理静态夹具。

| 检查 | 结果 |
| --- | --- |
| `go build ./...`（Task check 的实际命令） | 通过 |
| `go test ./...`（Task test 的实际命令） | 通过；最终 app 0.702s、commandanalysis 0.141s、config 0.024s |
| P4、P3、结构事实、配置、评测、既有 completion retry 定向检查 | 通过 |
| `go test -race ./internal/app ./internal/config ./internal/commandanalysis ./internal/evals -run 'Test(CommandOrganization\|CommandSimplification\|CommandStructureP4\|ReorganizationMetrics)' -count=1` | 通过；app 1.117s |
| `go test -tags commandtrace ./internal/app -run '^TestCommandAnalysisWorkBaseline$' -count=1` | Linux 通过，0.023s；历史快照未改动 |
| `git diff --check` | 通过 |

新增回归覆盖 off/once × auto/always/never × normal/minimal、三轮 registry/caller 重建、32 个并发预算竞争与两个实际并发分发入口、API retry/checkpoint、新请求及旧回调/完成事件隔离、取消零 dispatch、人工 S 与 N、原 call ID、来源/读取/文件脚本/MCP 排除、混合未知写入、不同赋值目标、嵌套宿主与独立宿主区别、配置默认值/环境覆盖/YAML/严格保存及评测事件防文本冒充。现有 P3 媒体/兄弟调用与审批回归保留。

审查中修复了前缀来源遗漏、单个宿主过宽触发、丢失动态参数旁的字面标志、PowerShell `$` 赋值格式和旧请求结束事件影响新状态的问题。测试未放宽授权断言，未更新历史观察快照。

## 限制

- 高置信门槛是有意收窄的首批实现；解析失败、unknown 单命令、单纯长度/标点/多动作、现有脚本和来源不明调用不自动触发。嵌套在字面字符串内部而没有独立 AST/IR 节点的宿主不猜测其层数。
- 原生写入来源账本、用户自然语言“原样执行”语义识别及 P5 真实模型/跨平台/人工终端评测未实现。
- PowerShell 新事实有合成 IR 覆盖；未运行真实 Windows PS5.1/PS7、Linux PowerShell 或 macOS。上述 Linux 结果不等于跨平台实机验收。没有真实模型收益数据，因此默认值继续 off。
