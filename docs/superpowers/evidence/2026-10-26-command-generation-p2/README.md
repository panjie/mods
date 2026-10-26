# P2 首批：结构诊断、CSV 读取与单层聚合

日期：2026-10-26。变更前代码为 `d34fe20`。本轮按用户要求只实施 P2a/P2b 的最小一批，不修改提示词、审批界面、规则匹配、审批策略或简化/重试机制。

## 实施边界

P2a 新增 `CommandAssessment.Structure`，直接复用同一次解析的 POSIX 节点、PowerShell IR v2 节点及已有 invocation facts。记录控制流深度、脚本块深度、赋值节点数量、可见 shell 宿主/内联载荷、已建模/未建模调用数量及位置。位置明确区分 POSIX byte 与 PowerShell UTF-16；无位置的旧/合成 IR 使用命令级诊断。最多保存 128 条 finding，超出时设置 Truncated；Clone 深复制。新诊断 code 与会改变写覆盖率的 AddGap 分开，不用于授权、拒绝或重试。

这批元数据不是完整数据流证明：赋值数量只统计赋值节点，不把变量读取算作状态，也不声称覆盖自增等全部状态操作；不递归解析内联宿主载荷。调用已建模不代表包含它的整个子树安全。未知命令、方法表达式、控制流可以各自定位，原有整体效果/覆盖判断仍有最终决定权。

P2b 只新增 `powershell/import-csv` catalog entry（revision 1），绑定通过的文件输入产生 `TargetRead / OperationRead / Literal` 事实，参数值如 encoding/delimiter 不进入读写目标。未新增内置工具或“脚本一律安全”规则。原先单层读取聚合已经支持，因此保持其语义，补足来源、子树及调用组合的负例。`foreach-object` entry 升至 revision 2，去掉“出现脚本块即可接受任意参数”的宽泛匹配。

## 支持表

| 形式 | 本轮处理 |
| --- | --- |
| `Import-Csv amounts.csv` | 一个静态标量文件路径；只读 |
| `Import-Csv -Path 'space name.csv'` | 精确参数名；只读；Path 不接受通配符 |
| `Import-Csv -LiteralPath 'file[1].csv'` | 方括号是字面路径；只读 |
| `-Delimiter ';'` | 单个 UTF-16 字符；不接受动态转换或表达式 |
| `-Encoding utf8` | ascii/bigendianunicode/default/oem/unicode/utf8/utf7/utf32；大小写不敏感 |
| CSV → 静态过滤/分组/Measure-Object/Select-Object | 使用已有组合检查；必须所有子调用均通过 |
| Get-ChildItem → ForEach-Object `{ Get-Content -LiteralPath $_.FullName }` → Measure-Object | 已支持；本轮补回归；整个来源链及子命令仍接受原有检查 |
| `ForEach-Object -Process { ... }` | 精确 Process、一个脚本块；同样检查全部子节点 |
| CSV 多路径/数组、变量/管道绑定路径、splat、冒号参数、缩写、重复路径、provider 路径、Header/UseCulture/未知参数 | 本轮不证明只读，进入普通审批；部分合法只读形式有意留待后续精确建模 |
| ForEach 的 Parallel/AsJob/Begin/End/多个脚本块/未知选项 | 不沿用普通读取映射证明；普通审批 |
| 嵌套循环、.NET 调用、动态执行、未知来源/子命令、混入写入或文件重定向 | 不新增支持；不得因外层聚合而免审 |

没有扩大方法名白名单，没有把目录候选当写权限，没有改变临时目录豁免和保存规则的覆盖要求。原有其它 cmdlet 的参数语法未在这轮全面重审，不能把上述严格 CSV/ForEach 绑定理解为所有 PowerShell 命令均采用严格参数解析。

## 固定失败样本对照

输入为 P1 精修 `final-normal/`、`final-minimal/` 中的四个原始 JSON 文件，只重放 A1（当前提示词）产生的 shell_run/powershell_run 调用，包括首轮和完成会话。每条记录保留来源 SHA-256、场景、repeat、调用序号和完整命令；共 52 条。对照通过生产分析器及 `commandResultToIntent → ClassifyAccess`，没有模型请求，也没有执行模型脚本。

| 模式 | shell 调用数 | 修改前 ask | 仅 P2a ask | P2a+P2b ask |
| --- | ---: | ---: | ---: | ---: |
| normal | 24 | 13 | 13 | 13 |
| minimal | 28 | 6 | 6 | 3 |
| 合计 | 52 | 19 | 19 | 16 |

三处变化全部是 minimal CSV repeat 1/4/5 的同一条命令：

```powershell
Import-Csv amounts.csv | Measure-Object -Property amount -Sum | Select-Object -ExpandProperty Sum
```

它们从 unknown/ask 变为静态 read/allow。固定合成数据的真实命令执行结果是 10。其余 49 条调用的 effect、coverage、decision 完全不变；原始复杂代码统计仍为 unknown/ask，不能声称 normal 物理行数问题已经修好。受支持的单层读取管道仍返回三个文件、6 个物理行；`Measure-Object -Line` 返回 4，明确保留语义差别。

这些是**固定调用的审批需求变化**，不是重新生成的完整会话成功率或交互 UI 审批展示次数。旧轨迹在一次调用获准后可能改变后续模型行为，因此不据此重算任务轮次、正确率或“零审批完成率”。native/process 调用不计入 52 条分母。首轮场景名含 hidden-write/nested-code 不表示模型实际输出了危险命令；安全性由独立负例断言，不依赖场景名称。

快照（原快照保留）：

- [修改前重放](../../../../internal/app/testdata/command_generation/p2_replay_before_windows.json)
- [仅结构诊断重放](../../../../internal/app/testdata/command_generation/p2_replay_structure_windows.json)
- [首批语义扩展后重放](../../../../internal/app/testdata/command_generation/p2_replay_after_windows.json)
- [P2 固定场景快照](../../../../internal/app/testdata/command_generation/baseline_p2_windows.json)

前两份是在语义扩展前实际运行取得，并非事后改写。P0 的 baseline_windows.json、原 cases.json、catalog_legacy.json、既有阶段快照均未覆盖。P2 固定场景只将 CSV 的独立预期改为 allow，其它断言保留。

## 安全与验证

新增测试覆盖：CSV 参数/路径绑定与 target 角色、原生进程/伪 cmdlet 身份、未知参数/缩写/重复参数/数组/provider/splat/动态求值；聚合中插入删除、写文件、重定向、启动进程、网络、未知调用、方法、嵌套循环和未绑定变量；保存规则及临时目录无法放行不完整写入；结构诊断不改变授权比较、不额外执行语义规则、Clone 隔离、确定性、128 条上限、旧结果缺字段及 UTF-16 源码位置。

真实 Windows PowerShell 5.1 与默认 PowerShell 7.4.14 均通过同一组聚合正负例的生产 bridge 解析。负例只解析，绝不执行。真实执行仅使用预先写定的读取命令和合成文件；没有 Linux/macOS 运行证据。

验证入口：

```powershell
go test ./internal/commandanalysis ./internal/approval ./internal/app -count=1
go test ./internal/app -run 'TestCommandGenerationP2|TestCommandGenerationBaseline|TestGenerationPowerShellAggregationSemantics|TestCommandAnalysisBaseline|TestCommandAuthorizationComparisonIgnoresPresentation' -count=1
go test -tags commandtrace ./internal/app -run '^TestCommandAnalysisWorkBaseline$' -count=1
go test ./internal/commandanalysis -run '^TestP2' -count=1 -v
go run github.com/go-task/task/v3/cmd/task@v3.51.1 check
go run github.com/go-task/task/v3/cmd/task@v3.51.1 test
```

三个 stage-6 fuzz 入口各运行 10 秒、parallel=2，检查解析/组合/确定性、未知选项、literal argv/稳定合并。work baseline 保留每次 Analyze 一次主解析的断言。

重放基线的 before/structure 文件没有更新入口；`-update-command-p2-replay` 只能在独立差异断言通过后更新 after。P0/P1 实时模型评测仍保留旧分析器哈希保护，必须在 `d34fe20` 等对应版本运行，不能在 P2 上伪称“只改变提示词”的 A/A1。若后续做新的模型端到端 P2 实验，需要单独记录分析器版本并保持提示词一致。

后续重点仍是复杂行数脚本的生成质量；本轮不靠放行任意循环/.NET 脚本解决它。P3 审批解释和 P4 简化机制继续留待单独实施。
