# P3 审批解释、人工简化与基础脚本预览

日期：2026-10-27。基线提交：`62c3ab8`。在已有未提交 P3 实现上审查、修补并验证；代码和本记录一同提交。

设计：[总体设计](../../specs/2026-10-26-reviewable-command-generation-design.md)；计划：[P3a–P3c](../../plans/2026-10-26-reviewable-command-generation.md)。

## 完成范围

- P3a：区分未知效果和已知写入但目标覆盖不完整；展示读取、写入、候选、结构位置、实际工作目录及解释器调用参数。源码编号，完整 argv 保留边界，继续使用原分页和全页浏览门槛。UTF-16/UTF-8 偏移缺失、越界或落在字符中间时降级到命令级，不伪造行号。结构诊断截断明确标注。
- P3b：Y/N/S 使用显式枚举，零值和未知值拒绝；S 仅在命令审批提供，不需要先看完所有页，不保存授权、不执行原调用。通过有类型错误返回 `mods.command_reorganization`、`not_executed`，保留原 call ID 和 `IsError=true`；活动状态区分人工组织反馈与执行失败。Ctrl+C 保留任务取消，不返回普通拒绝让模型继续。
- P3c：交付设计允许的**基础预览模式**。只在原本需要人工审批且有交互通道时准备文件内容，最多 256 KiB，独立两秒预算；同时最多两个文件读取 worker，防止不可取消的 OS 文件操作无限堆积。预览不是效果证明，不产生新的免审规则。
- 执行前检查文件身份、内容摘要、大小、修改时间和绑定上下文。文件变化和既有目标变化共享同一个重审批次数上限；变化一次重新准备/审阅，持续变化停止。执行始终使用原路径和参数。

## 预览支持边界

| 调用形式 | 行为 |
| --- | --- |
| `process_run` 的 python/python3/py、node、ruby、perl、sh/bash/zsh/ksh，首参数为脚本 | 读取字面文件，不展开 `$`、`~` 或去除引号 |
| `process_run` 的 pwsh/powershell，精确 `-File path` | 支持脚本参数，以及前置精确 `-NoProfile/-NonInteractive/-NoLogo` |
| 未审计解释器选项、缩写、stdin、模块/内联代码、Windows 驱动器相对路径 | 不绑定文件预览，保留普通审批 |
| Shell 字符串中调用文件、直接执行脚本或其它解释器 | 分析器标识为脚本时明确显示预览不可用；不二次解析或猜测执行文件 |
| 文件缺失、不可读、非普通文件、超限、二进制、UTF-16/无效 UTF-8、准备超时/reader 忙 | 明确未审阅全文，仍可普通未知程序审批；请求取消则停止 |
| UTF-8 或 UTF-8 BOM | 原始字节计算摘要，展示脱敏编号源码 |
| ReviewNever、已证明读取、显式信任、合法规则免审 | 不读取脚本、不新增预览阻断 |

没有接入可靠的“本轮原生写入生成”来源账本，所有文件来源标为 unknown；不宣称交付完整载荷来源追踪模式。脚本正文、参数和路径均为不可信展示数据，已知秘密及 URL 凭据/查询脱敏，终端控制字符转义。入口文件复核不能冻结导入、子进程或文件系统，也不消除 TOCTOU。

## 本次审查修复

1. 原预览路径使用 Shell 展开，可能读取与 literal argv 不同的文件；改为字面路径并保留 `link/../file` 的 OS 解析顺序。
2. 原界面只显示 PowerShell/POSIX 泛称，且省略绑定的 host 参数；复用执行路径构造元数据，不启动解释器。
3. 预览及其它展示行统一脱敏；在编号/转义之前遮罩秘密，避免多行秘密被显示变换拆开。
4. 取消时旧 requestApproval 返回“用户拒绝”；改为传播 context 取消，并使取消优先于同时到达的响应/会话关闭。
5. 预览读取增加 worker 数量上限、普通文件检查和读取期间变化检查；准备超时降级，任务取消不降级。
6. 原生 argv 不使用有损命令预览推算源码位置；补字符边界和诊断截断处理。

## 验证与限制

新增 `p3_review_test.go`，配合 `command_payload*_test.go`、`command_dispatch_test.go`、`review_layout_test.go`，覆盖字面路径、同字节文件身份替换、一次/持续变化、编码/超限/缺失、取消、人工简化零分发、原 call ID、兄弟调用与媒体契约、零值拒绝、源码 Unicode 和控制字符。

定向检查中发现并修复 Ctrl+C 错误分类。并发启动多个测试进程时，一秒的通用审批测试等待窗口遇到真实 PowerShell 冷启动超时；改为五秒，未降低执行/授权断言。

验证环境：Windows amd64；本次执行结果如下。所有危险/未知命令只通过模拟 dispatch 验证，没有实际执行。

| 检查 | 结果 |
| --- | --- |
| P3 / review / payload / manual 定向测试 | 修复后通过；P3/payload/manual/P2Replay 组合 23.948s |
| `TestCommandGenerationP2Replay` / `TestCommandAnalysisBaseline` | 通过（后者包含在完整根模块测试）；不更新历史快照 |
| `TestCommandAnalysisWorkBaseline`（commandtrace） | Windows 通过，17.794s |
| Task check | `go run github.com/go-task/task/v3/cmd/task@v3.51.1 check` 通过 |
| Task test | `go run github.com/go-task/task/v3/cmd/task@v3.51.1 test` 全根模块通过；app 93.770s、commandanalysis 54.251s、tools 47.231s |
| race | 本机 CGO_ENABLED=0，无 gcc，未执行 |

本次为 Windows 的确定性测试和模拟交互分发验证；不等于人工终端理解成本评测，也不等于 Linux/macOS 实机验收。没有发出真实模型请求、执行模型未知脚本或运行 UAC 测试。

可复跑定向入口：

```text
go test ./internal/app -run 'Test(P3|Review|CommandPayload|ScriptPayload|ManualCommand|CommandGenerationP2Replay|CommandAnalysisBaseline)' -count=1
go test -tags commandtrace ./internal/app -run '^TestCommandAnalysisWorkBaseline$' -count=1
```

P3 降低必要审批的理解成本；不会仅凭界面改进减少 unknown 的授权门槛。历史 52 条命令重放仍用于验证权限不变，不伪造实际人工审批数。P4 自动简化、设置和请求级预算未实施，P5 的真实生成与人工/跨平台发布验收仍待开展。
