# 阶段 2：共享解析、上下文与有界查询

日期：2026-10-24。基于 `34b3423` 及阶段 1 收尾；阶段 1/2 修改已一起纳入本次本地提交，未推送。阶段 2 实现与 Linux 验证完成；Windows 真实 host、junction/复核和快照验收待补，不能据交叉编译宣称全平台完成。阶段 3–6 未实施。

## 交付内容

- `parsed.go` 的私有 `parsedCommand` 在每次 Analyze 中保存一次 POSIX AST 或 PowerShell IR。静态效果、诊断、调用枚举、heredoc 屏蔽、字面路径恢复和 POSIX 环境变化判断共享它。节点有 ID、父关系、类型、范围；POSIX 范围为字节偏移，PowerShell 为 UTF-16 偏移。嵌套调用仍单独枚举，不被已知外层调用覆盖。
- 删除 `ShellInput.InvocationDialect`；Git 使用同一方言和解析结果。显式只读信任的无信任对照仍可遍历同一树，但不会再次解析。命令语义的重复求值留到阶段 3 catalog 收敛。
- 每次 Analyze 复制环境与信任输入，所有目标物化和宿主查询使用该次上下文。环境使用一次完整快照而非逐变量懒读：固定 helper 需要完整、相同的子进程环境，这也避免读取时序不一致。secret_env 仅传名称，并阻止对应值参与目标证明；Git 配置或 PATH 被遮蔽时远端保持 unresolved。
- `Dependencies` 提供 `PathResolver`、`GitResolver`、`PowerShellParser` 和 `AutomaticResolver`。Git 只能接收枚举化查询（验证 helper、branch、push remote、rewrite 等），不能传任意 shell 或 argv。helper 从捕获的 PATH 解析，仍检查工作区/临时程序来源。
- 移除自动变量的进程级结果缓存；同次调用重复引用可复用，下一次 Analyze/审批后复核必须重读。Git 和真实路径也不跨调用缓存。
- PowerShell IR v2 新增节点关系、参数原文/字面性和文件重定向 owner/target/append/stream。新增字段仅为结构记录，本阶段不启用更宽松的重定向分类或完整写范围。
- bridge 仍复用进程，但按 host 和环境快照更换连接；等待锁、通信、固定 helper 接收取消信号，输出有界，损坏协议连接退役，关闭幂等。自动变量查询不执行用户提供的表达式，只接受 HOME/PROFILE 白名单形状。
- 初次分析与复核从 app 的带 error 适配入口调用。取消/超时错误传回 dispatch，三种 review 模式都零执行。语法无法证明的普通结果仍进入原有审批；预算超限返回 unknown/incomplete 和专用 gap，不生成免审证据。

## 有界范围

| 项目 | 当前界限 |
| --- | --- |
| Analyze 总 deadline | 15 秒；继承更早的调用方 deadline |
| 输入总文本 | 1 MiB，在复制可变容器前检查；PowerShell 保留原 64 KiB source 限制 |
| AST/IR | 100,000 节点、256 层；解析失败/结构不合法不提供完整性证据 |
| 宿主查询 | 每次 Analyze 最多 64 次，包括路径、仓库、helper 身份和自动变量查询 |
| helper/路径返回值 | 单次 64 KiB、累计 256 KiB |
| bridge JSON | 单条最多 4 MiB，独立于 helper 输出额度 |
| Git 查询组 / 自动变量查询 | 原有 1 秒 / 2 秒上限，并受总 deadline 约束 |

这不是进程沙箱。纯 Go parser 和本地文件系统 syscall 不能在任意指令点强制中断；输入/结构界限及查询前后 context 检查限制其范围，不能保证阻塞网络文件系统 syscall 在 deadline 瞬间返回。可注入实现也必须遵守 context 契约。

## 差分证据

为避免用新观测覆盖旧结果，在 `/tmp` 建立两个独立 checkout：迁移前分析代码取自 `34b3423`，另一份复制本轮阶段 2 修改。在两份目录中运行原始安全断言后记录 Linux 事实和计数，逐项比较。主工作区原有 Windows JSON 未改动。

Linux 23 个适用样本（17 POSIX、6 literal argv）的完整事实/审批 JSON **完全相同**。本次加入 `baseline_linux.json` 固化该结果；它不包含 Windows 的 8 个 PowerShell 样本。

| 观测项 | 阶段 1 | 阶段 2 |
| --- | ---: | ---: |
| 23 个样本初次 POSIX parse 总数 | 90 | 17 |
| 写调用复核 POSIX parse 总数 | 60 | 12 |
| 初次 Git 子进程查询数 | 1 | 1 |
| 复核 Git 子进程查询数 | 1 | 1 |
| Linux bridge parse / round trip | 0 / 0 | 0 / 0 |

阶段 1 的 trace 没有记录 `commandMutatesPOSIXEnvironment` 中的额外解析，因此 90/60 是原计数点观测，不是完整 profiler。阶段 2 的主解析计数覆盖完整生产 shell 流程，每个 POSIX 输入为 1，argv 为 0。

`work_stage2_linux.json` 保存新测量；阶段 0 的 `work_windows.json` 原样保留。work harness 无论平台是否存在测量文件，都断言每次 shell 分析只解析一次。Windows 阶段 2 工作量文件尚未生成，禁止用推测值替代真实 host 结果。未来实际记录须审查全部差异。

固定 corpus 之外的显式变化：

- Windows 显式 POSIX 分析不再为 Git 辅助枚举调用 PowerShell，避免旧方言漂移；需 Windows 实测确认事实差异。
- parser/预算/取消失败具有结构化 gap。新增预算可能使超长或大量查询的输入保守降级。
- secret_env 遮蔽 Git 配置/PATH 时不再读取继承配置猜测远端；helper 仅从捕获的绝对 PATH 项解析，无法确定时保持 unresolved。
- HOME 及 destination 目录物化延迟到本次环境快照，避免语法 helper 重新读取宿主状态；HOME 被遮蔽时不能借 USERPROFILE 等继承别名补全，保持 incomplete；literal argv 仍不插值。
- 自动变量结果不再跨调用缓存，环境改变时重新查询；不把旧 host 的结果用于审批后复核。

未改变 cp/mv 完整性、Git hook/helper 缺口、旧显式信任、规则范围或临时写政策。上述样本不是生产工作负载，不宣称线上误报率下降。

## 验证

环境：Linux amd64，Go `go1.27.3`，Git `2.56.0`。

通过：

```text
go build ./...
go test ./... -count=1
go test -tags commandtrace ./internal/app ./internal/commandanalysis ./internal/commandtrace -count=1
go test -race ./internal/commandanalysis ./internal/approval ./internal/app -count=1
GOOS=windows GOARCH=amd64 go test ./internal/commandanalysis -c -o /tmp/mods-commandanalysis-windows.test.exe
GOOS=windows GOARCH=amd64 go build ./...
git diff --check
```

沿用阶段 1 的 Task runner 依赖编译问题记录，本轮使用 Taskfile 中等价的原生 build/test，不修改产品依赖修复 Task runner。

新增验证包括：

- 带计数的单解析测试：读取、显式信任、heredoc、未知兄弟/嵌套调用、Git 组合、非法与空 source；复核再次解析。
- 注入 PowerShell IR 的共享解析/协议失败/节点关系测试，以及新增 Windows 真实 bridge v2 关系测试（仅编译，尚未执行）。
- parser/Git 取消、bridge 阻塞通信取消、输入/节点深度/查询次数/单次及总输出超限、路径失败均不产生 complete。
- 环境在分析中改变不影响当前目标，下一次 Analyze 看到新值；Git 多 URL 全部保留、复核不使用旧 origin、遮蔽配置不查询。
- 实际 app dispatch 在取消时拒绝执行，包括 ReviewNever；已有目标变化重新审批和持续变化失败关闭测试随全量测试通过。

尚缺：Windows 真实 pwsh/5.1 host 与最新事实/work 快照、Windows junction/生命周期执行、macOS。未执行 provider integration、UAC、远端 push。交叉编译和合成 IR 都不能代替真实 Windows 证据。

## 后续入口

先补 Windows 阶段 2 真实回放及差分审核。阶段 3 再建立统一 catalog、消除同一 invocation 的重复语义求值；阶段 4 再切换角色化目标。legacy Result/KnownDirs、approval 门面及 focused Git 测试入口仍在，不是第二套生产分析实现；其删除清单继续参考阶段 1 收尾记录。
