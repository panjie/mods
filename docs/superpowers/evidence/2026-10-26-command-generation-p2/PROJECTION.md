# P2b 第二批：聚合结果的标量属性读取

日期：2026-10-27。基线：`f21448f`。继续 P2b，不修改提示词、审批界面、授权策略或简化机制。

## 为什么选择这一批

检查首批重放后仍需审批的 16 条调用：主要阻断来自顶层缓存赋值、嵌套循环及 .NET 方法，静态过滤、投影、分组本身已经支持。本批不扩大这些复杂形式的权限，先补一个简短写法缺口：`(Get-Content file | Measure-Object).Count` 在修改前真实 bridge 测试中为 unknown。

现在只接受**顶层、单个括号内管道、最后一个调用有已审计输出类型、静态标量属性**的形式。整个管道仍须通过所有原有调用、变量、方法、重定向、赋值及控制流检查。

## 规则边界

| 形式 | 结果 |
| --- | --- |
| `(Get-Content file | Measure-Object).Count` | 只读 |
| `(Import-Csv amounts.csv | Measure-Object -Property amount -Sum).Sum` | 只读 |
| `(Get-ChildItem -File | Where-Object Extension -eq '.go' | Measure-Object).Count` | 复用已有静态过滤语义；只读 |
| `(Get-ChildItem -File | Group-Object Extension | Measure-Object).Count` | 复用已有分组语义；只读 |
| `(Get-ChildItem ... | ForEach-Object { Get-Content -LiteralPath $_.FullName } | Measure-Object).Count` | 单层读取映射通过全部检查时，只读 |
| Measure 无参数或单个精确 `-Property` | 证明 Count；属性参数限单个静态标识符 |
| Measure 的 `-Sum/-Average/-Minimum/-Maximum` | 额外证明对应输出字段；参数不重复 |
| Measure 的 `-Line/-Word/-Character` | 只证明请求的 Lines/Words/Characters；不证明 Count，不与数值参数混用 |
| 缩写、未知参数、数组/计算属性、InputObject/OutVariable、冒号参数 | 不产生新的属性证明 |
| 任意 `.PSObject`、动态属性、方法调用、属性写入、嵌套语句、非 Measure 输出 | 不新增只读支持 |
| 混入未知命令、动态执行、文件写入或重定向 | 不继承外层聚合的只读证明；按原有写入/unknown 策略审批 |

已知、目标完整的写入仍可以使用已有写规则或临时目录豁免；不能将这类合法授权误认为“被聚合绕过”。负例分别测试未授权的明确写入，以及即使给出目录规则也不能放行的不完整写入。

## 实现

`measure-object` catalog revision 2 增加输出字段规则，结果保存在每次分析的 `InvocationFacts.ReadProperties`。没有修改它原有的普通调用读取规则：新的属性投影证据比旧的调用读取判断更严格。

`powershell_projection.go` 从共享 IR v2 验证 MemberExpression → ParenExpression → Pipeline 的关系、顶层所有权、最终 CommandAst 节点及对应语义缓存，再用 UTF-16 半开区间核对属性和完整表达式。显式 trust、缺失节点、旧版本 IR 和未知输出类型都不能替代此证据。没有第二次解析、方法名全局白名单或运行源码的探针。

UTF-8 正例暴露了原 bridge 的环境依赖：Go 发出的 `中文😀.txt` 被当前主机控制台代码页错误解码，AST 内容及位置随之失真。`ps_bridge.ps1` 现在明确设置 Console InputEncoding/OutputEncoding 为无 BOM UTF-8。PowerShell 5.1 与默认 PowerShell 7 均验证了原文、路径参数和表达式的精确往返。

## 验证结果与限制

最终 `task check`、`task test` 全部通过（最终 app 49.756s）；commandtrace 工作量基线通过，未知参数 fuzz 10 秒/parallel=2 通过。首次全量运行发现新快照保留了随机测试目录父路径，已仅将该已知 fixture 身份归一化为 `<ROOT>` 后重跑通过，没有放松路径或审批断言。

- 新增聚合投影正负例在真实 PowerShell 5.1/7 bridge 均通过，负例仅解析，不执行。
- 静态过滤、分组、单层映射及 CSV 聚合的 11 个 app 授权控制场景通过。新快照为 [projection_windows.json](../../../../internal/app/testdata/command_generation/projection_windows.json)，首批及 P0/P1 快照保留。
- 真实固定命令执行：CSV `.Sum` 为 10，代码物理行 `.Count` 为 6；原有 `-Line` 测试仍为 4，不能混淆统计口径。
- `TestP2ProjectionSharesParseAndFacts` 验证只消费已有语义事实、每次 Analyze 一次主解析、重新验证重新解析，缺失/错误 IR 不能放行。
- 52 条历史 shell 调用重放保持首批结果：19→16 ask；**本批没有进一步减少这些旧样本的审批**。这批支持的是新增的简短替代写法，不能声称原复杂脚本或完整任务成功率得到改善。
- 未运行新的模型端到端对照，也没有 Linux/macOS 实机证据。

复核入口：

```powershell
go test ./internal/commandanalysis -run '^TestP2' -count=1
go test ./internal/app -run '^TestP2ProjectionAccess$|^TestGenerationPowerShellAggregationSemantics$|^TestCommandGenerationP2Replay$' -count=1
go test -tags commandtrace ./internal/app -run '^TestCommandAnalysisWorkBaseline$' -count=1
go test ./internal/commandanalysis -run '^$' -fuzz '^FuzzAuditedOptionsNeverHideUnknownFlag$' -fuzztime=10s -parallel=2
go run github.com/go-task/task/v3/cmd/task@v3.51.1 check
go run github.com/go-task/task/v3/cmd/task@v3.51.1 test
```

后续若处理顶层缓存变量，应先设计来源及使用绑定，不把已有调用级只读证明直接传递给任意变量/对象。P3 审批解释与 P4 简化仍需单独实施。
