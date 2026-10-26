# 命令分析 Windows 实机验收

日期：2026-10-26。验证基于周末 Linux 开发的 `33dd565`，从 `34b3423` fast-forward 同步。本轮补齐 Windows 测试、平台快照和验证脚本，不扩大生产授权语义。

## 环境与修复

- Windows 11 amd64（10.0.22631），Go 1.26.3，Git 2.55.0.vfs.0.10，Intel i5-13500。
- PowerShell 7.4.14 与 Windows PowerShell 5.1.22621.7649 均实际运行。
- 两个分析测试使用真实宿主临时目录代替 Linux `/tmp`、`/workspace` 假设；混合读写测试继续检查唯一写目标和解析后的父目录。
- 未知命令展示测试显式选择 POSIX，因为它断言 POSIX 专用诊断信息。
- Windows 专用测试更新到阶段 4/5 的目标角色与完整性语义：变量、自动变量、环境和数组展开可以提供展示路径，但不证明标量写绑定。保留 Ask，并增加不能保存规则/完整授权的断言。注册表展示分别断言读源与写目标。未以放宽生产转换来消除失败。
- `scripts/windows-reliability.ps1` 补入迁移后的 commandanalysis 包；5.1 通道移除 PATH 上所有含 pwsh.exe 的目录，避免系统安装、运行时安装和 WindowsApps 别名导致误用 PS7。初次运行暴露该问题，修复后重新运行通过。

## 验证结果

| 检查 | 结果 |
| --- | --- |
| Task `check`：`go build ./...` | 通过 |
| Task `build` / `bin/mods.exe --version` | 通过，生成 v4.5.0-32-g33dd565-dirty |
| Task `test`：`go test ./...`，默认 PS7 | 通过，app/commandanalysis/cli 实际执行 |
| 隔离 PATH 后 PS5.1 的 `go test ./internal/commandanalysis ./internal/app ./internal/cli -count=1` | 通过 |
| commandtrace 基线、共享解析与计数器测试 | 通过 |
| 新 Windows 事实/work 快照独立重放 | 通过，未使用 update |
| `TestStage5RealPowerShellBindings`，分别指定两个 host | 每个 host 15 条解析、7 条隔离执行正例通过 |
| Windows reliability quick，PS5.1 / PS7 | 两个通道退出码 0，已执行测试均通过；原始报告保存在 `artifacts/windows-command-analysis` |
| `git diff --check` | 通过 |

真实命令执行限于测试临时目录及既有本地可靠性 fixture；没有 provider API、网络 push 或 UAC 集成。PS5.1 完整包测试包含 bridge 清理、审批拒绝、重审与 Windows 路径回归。reliability 覆盖 junction 边界、进程树取消、编码及执行程序绑定。

UNC 未配置，因此 UNC 测试跳过；PS5.1 的包管理器 shim 测试因宿主隔离后的工具可用性跳过。报告中的 production-ready=false 反映这些缺口，不代表已执行测试失败。本轮未跑 Windows race（无 gcc）、长时间 stress 或重新 fuzz；Linux 的既有结果不能替代这些 Windows 检查。macOS 实机验收仍待完成。

## 快照审查

新增 `baseline_stage5_windows.json`（31 条）及 `work_stage2_windows.json`。原 `baseline_windows.json` / `work_windows.json` 保持不变，人工安全断言保持不变。

与阶段 0 Windows 快照共有字段比较：

- 仅 `ps-redirect` 从 Ask 变 Allow：阶段 5 已实现的字面 AST 重定向，且规则覆盖输出目录；effect 从 unknown 变 write，coverage 完整。
- `ps-unknown-temp-redirect` 现在保留重定向写事实，但未知调用仍使 coverage 不完整，继续 Ask。
- `ps-provider` 缺口精简，但 provider 仍不能复用文件系统规则，继续 Ask。
- `posix-copy-mixed` / `posix-move-source-ungranted` 增加 unbounded-write 缺口，继续 Ask。Windows 的文件身份/卷证明尚未实现（transfer_other.go），不把 Linux 的 cp/mv 自动批准推广到 Windows。
- 新字段明确记录 read_dirs、write_dirs、candidate_paths 和 intent_coverage；候选信息不作为写权限。
- 其余共有事实及全部独立 safety 决策不变。

| 31 条固定样本合计 | 初次：旧 → 新 | 复核：旧 → 新 |
| --- | --- | --- |
| POSIX parse | 73 → 17 | 48 → 12 |
| bridge parse / round trip | 33 → 8 | 26 → 7 |
| Git query | 2 → 2 | 2 → 2 |

同一 Analyze 只进行一次主解析，复核重新解析；显式 POSIX 输入不再额外调用 PowerShell。计数是固定构造样本，不是生产误报率。

## 冷热桥基准

`BenchmarkPowerShellBridgeLifecycle`，每项 5 次；cold 包含启动/关闭，warm 每轮真实解析。原始输出保存在 `artifacts/windows-command-analysis/bridge-ps*-benchmark.txt`。

| Host | cold | warm |
| --- | --- | --- |
| PS7 | 581.14 ms/op | 3.01 ms/op |
| PS5.1 | 353.74 ms/op | 3.66 ms/op |

只记录本机量级，不据此宣称统计显著的性能改进。
