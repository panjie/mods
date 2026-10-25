# 阶段 5 后续命令族实施与验收

日期：2026-10-25。基于 `54060c5`，后续与第六阶段合并到同一本地提交。承接[首批记录](2026-10-25-command-analysis-stage5.md)，补齐第五阶段预定命令族中的文件有值参数、受限 wrapper 写入、Git 全局选项、PowerShell 标量写入与可靠重定向。本文保留第五阶段实施时的验收记录；最新清理与平台状态见 [第六阶段记录](2026-10-25-command-analysis-stage6.md)。

## 实现边界

| 命令族 | 新支持及收紧 | 不推定完整的形式 |
| --- | --- | --- |
| 文件写（native revision 4） | 显式声明的无值短选项可组合，如 touch -am、tee -ai；touch -r/--reference 的参数为读取来源；-d/--date、-t、--time 消耗值，支持声明过的附着值 | 未知选项、混合值选项 bundle、递归、-h；单独 `-` 不作为普通写文件，避免借用路径规则授权 stdout FD |
| env（native revision 2） | 原有 LANG/LC_/TZ 等安全赋值集内，精确复用内层目标、读写角色和操作类型；支持嵌套受限 env；rm 保留 remove-entry，参考路径保持 read | PATH/加载器变量、-S、动态命令、未知内层、宿主证明依赖的 cp/mv；组合中的未知调用仍保留 gap |
| Git（revision 2） | 共用 `bindGitGlobals`，支持重复 -C、--no-pager、--literal-pathspecs、--no-optional-locks；读取、仓库目录展示和 push origin 查询使用同一绑定 | -c、--git-dir、--work-tree、外部 pager；仓库写即使目标已知也因 hook/helper 未建模而 incomplete |
| PowerShell 内容写（revision 2） | Set-Content/Add-Content/Out-File 及既有别名绑定一个标量输出；完整名称的 LiteralPath/Path/FilePath、Value、Encoding、Width 等以及适用的 Force/NoNewline/Append 等开关 | 未知/缩写参数、重复路径、数组、splat、动态路径、provider、Stream、需解码的转义/内嵌引号；Path 通配符不因引号而变为字面路径，LiteralPath 单独保留字面证据 |
| PowerShell 重定向 | IR v2 的 FileRedirectionAst、owner、stream、literal target 一一对应后加入 follow-leaf 写目标；支持明确的 >、>>、错误流和全流目标 | 只有旧标签、缺失/重复/错误节点、动态/通配/provider 目标、赋值或复杂控制流；已知重定向不掩盖未知命令 |

参数绑定是事实证明，不直接授权。app 仍唯一通过 `commandResultToIntent` 和普通审批入口决定；未增加模型请求、工具名规则或 shell-prefix 权限。

语义依据：[GNU touch](https://www.gnu.org/s/coreutils/manual/html_node/touch-invocation.html)、[Git 全局选项](https://git-scm.com/docs/git)、[PowerShell 重定向](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.core/about/about_redirection?view=powershell-7)、[Set-Content](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.management/set-content?view=powershell-7.4)。表中的支持范围小于命令完整语法，不以猜测补全。

## 前后固定样本

新增独立 `cases_stage5_extended.json`，10 条样本的 Required 决策为手工安全断言。`-update-command-baseline` 只记录观测，断言失败不能生成快照。

基准端从 `54060c5` 隔离 worktree 执行相同样本与 harness，保留 `baseline_stage5_extended_before_54060c5_linux.json`；当前端为 `baseline_stage5_extended_linux.json`。没有重录原 `cases.json`、23 条阶段 5 首批快照、旧平台 work 快照或 1,032 条 catalog 迁移观测。

| 样本 | Auto 前 → 后 | 原因 |
| --- | --- | --- |
| touch-reference、touch-reference-equals | Ask → Allow | 参考文件是 read；唯一写目标在已授权目录；参数完整绑定 |
| env-touch、env-remove | Ask → Allow | 安全赋值不改变内层写目标；完整复用 touch/rm 事实 |
| git-global-read | Ask → Allow | 受支持全局参数后仍是已建模读取 |
| env-unsafe-assignment、env-unknown-sibling | Ask → Ask | 环境重绑定或未知效果保留缺口 |
| touch-unknown-bundle、touch-missing-value | Ask → Ask | 未知/缺值参数不证明完整目标 |
| git-global-config | Ask → Ask | 配置注入未建模 |

新增集为 5 条 Ask→Allow、0 条 Allow→Ask；原 23 条样本事实不变。另有明确安全收紧：PowerShell 旧 catalog 中未绑定的内容写、未知参数以及 Out-File 多个位置操作数不再被当作 complete；迁移测试中列出差异，旧 JSON 不变。过去 `env touch out` 的限制测试改为不安全 PATH 赋值，普通 env 写由独立正反例与真实 dispatch 测试接管。

暖启动 benchmark（Linux amd64、同机顺序运行、每样本 200 次、单轮观测）：

| 样本 | 前 ns/op | 后 ns/op |
| --- | ---: | ---: |
| touch-reference | 113813 | 114890 |
| touch-reference-equals | 103324 | 105669 |
| env-touch | 95799 | 82935 |
| env-remove | 85900 | 79814 |
| git-global-read | 83014 | 74476 |

完整输出在 `stage5_extended_benchmark_linux.txt`。这些单轮耗时不是统计性能结论；构造样本的审批改善不能当作真实用户误报率。

## 验收证据

- Linux `go build ./...`、`go test ./...` 通过。
- commandtrace 三包、commandanalysis/app/approval race 通过，旧 work 快照不变。
- Windows amd64 根模块和 commandanalysis/app 测试交叉编译通过；真实 Windows 执行尚未完成。
- `TestStage5TransferApprovalDispatch` 经生产审批、复核、dispatch 在隔离目录实际执行 touch/受限 env；参考文件不需要写规则；未知参数、PATH 修改和未知内层均零 dispatch。
- `TestStage5PowerShellAnalysisApproval` 将 IR→事实→生产转换→真实审批入口串联；只有已授权目标且完整事实放行。
- `TestStage5PowerShellDispatch` 是新增 Windows 实际桥与 dispatch 用例；本机因平台跳过，不记作通过。
- 官方 PowerShell 7.6.8 可移植包仅解压到 `/tmp/mods-stage5-pwsh`。使用原 `ps_bridge.ps1`，`MODS_TEST_PWSH=/tmp/mods-stage5-pwsh/pwsh go test ./internal/commandanalysis -run '^TestStage5RealPowerShellBindings$' -count=1 -v` 通过 15 个真实解析样本；其中 7 个正例另外在隔离目录执行，并检查预测的写目标确实存在。包含 LiteralPath 方括号、Out-File、多重重定向；数组/splat/provider/通配/未知命令等负例只解析，不执行。
- 分析器生产宿主选择未改变：Windows 仍选择实际执行 shell 对应的 PowerShell；Linux 的可选测试通过依赖注入运行同一脚本，不将 Linux PowerShell 当成 Windows 路径、junction、5.1 或 UAC 的证据。

## 后续边界

第五阶段预定命令族已有本轮明确支持集和负例；未审计形式属于显式保守边界，不会自动放行。仍需真实 Windows/macOS 运行与对应平台快照，尤其 Windows PowerShell 5.1、路径和 junction；Linux PowerShell 7 验证不能替代这些项。cp/mv 递归、链接、跨卷，复杂 PowerShell 绑定，Git 配置/helper，任意 wrapper 写入都不在当前可复用授权范围内。第六阶段兼容清理已在后续完成，全平台实机验收仍待补，详见第六阶段记录。
