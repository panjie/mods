# 阶段 0：命令分析基线

日期：2026-10-23。本报告保留阶段 0 测量时的状态；后续模块迁移与 Linux 验证见 [阶段 1 收尾](2026-10-24-command-analysis-stage1.md)。
关联：[设计](../specs/2026-10-23-command-analysis-catalog-design.md)、[实施计划](2026-10-23-command-analysis-catalog.md)。

## 1. 测量范围与环境

- 基础 revision：`c502ea25125ae68888a9fa7b116d10657e76b65f`；测量包含本阶段新增测试和计数点，没有改变分类/授权算法。
- Windows amd64，Go `go1.26.3`，Git `2.55.0.vfs.0.10`，实际解析 host 为 PowerShell `7.4.14`。
- CPU：13th Gen Intel Core i5-13500。
- 样本全部来自现有回归和人工构造，不是线上日志；未收集真实用户工作负载，不能据此推算生产误报率。
- 所有 Git 仓库位于临时 fixture，隔离继承的 `GIT_*` 和全局/系统 Git 配置。远端仅使用 `.example` 名称，执行固定本地配置查询，绝不实际 push。
- 待分析的 shell/argv 从不执行。链接、实际落盘和 dispatch 行为由现有端到端测试继续验证。

## 2. 可回放资产

| 资产 | 内容 |
| --- | --- |
| `internal/app/command_baseline_test.go` | schema 校验、生产分析入口、授权矩阵/保存规则组合、事实快照比较、工作量计数和基准 |
| `internal/app/testdata/command_analysis/cases.json` | 31 个输入、方言、授权范围、secret_env 遮蔽名称、来源和人工安全断言 |
| 同目录 `baseline_windows.json` | effect/evidence/coverage/gaps、已知目录、叶子写路径、动态路径、origin/provider、三种审批模式结果 |
| 同目录 `work_windows.json` | 各输入的初始分析及写调用复核式再次分析的 parser/bridge/Git 查询计数 |
| `internal/commandtrace` | `commandtrace` build tag 下启用的原子计数器；默认构建为空操作，不存储命令或路径 |

`<WS>`、`<EXT>`、`<SAFE>` 为每次运行生成的独立目录，保存的结果不包含用户路径。只有 `<SAFE>` 被传入本次策略检查的临时豁免集合；不能把整个测试根目录自动作为安全范围掩盖越权。

样本分布：POSIX 17、literal argv 6、PowerShell 8。分类为读取 7、已授权/安全临时写 3、安全反例 18、已知限制 3。

人工断言与兼容快照分开：非 limitation 样本必须显式声明 required decision；安全样本必须 ask。Git 多 push URL 另有全部 origin 的人工断言。`-update-command-baseline` 不修改这些断言，断言失败拒绝更新事实快照。

compatibility 快照仅记录已实际运行的 Windows 结果；其它系统仍运行适用的安全断言，不能据此宣称事实等价已经验证。后续 CI 收集各平台快照时必须人工审查差异，不能默认复制 Windows 文件。

## 3. 观察结果

| 分组 | 数量 | 当前结果 | 解释 |
| --- | --- | --- | --- |
| 已支持读取 | 7 | 7 allow | 此组用于保护现有快速路径，不代表覆盖所有读取 |
| 已授权/安全临时写 | 3 | 3 allow | 同时保护规则复用和 temp 路径 |
| 安全反例 | 18 | 18 ask | 错误自动放行为 0；更新快照不能改变 required=ask |
| 已知限制 | 3 | 3 ask | 需结合具体语义理解，不能把三者全部当误报 |

三个限制：

1. `cp <EXT>/input.txt <WS>/output.txt`，只授权 WS：提取到输出，coverage 仍 incomplete。这个受控普通文件 fixture 是后续减少重复审批的目标。
2. PowerShell `Write-Output hello > <WS>/output.txt`，只授权 WS：当前 effect unknown，重定向的目标绑定不足；后续完善 IR 和目标语义。
3. `Get-Content ...; git push origin`：前面的非 Git 调用会使远端解析上下文失效。改善只读上下文传播可以恢复远端展示，但 hook/helper 未界定时 push 仍应询问，不能把最终 ask 算作误报。

另一个重要事实：`mv <EXT>/input.txt <WS>/output.txt` 当前主要记录目的地，依靠 incomplete 阻止仅目的地规则放行。后续不得只把 mv 加入“完整写命令集合”；先补齐源目录项写。

## 4. 重复解析的计数基线

计数点覆盖当前生产代码中五处 POSIX `syntax.NewParser` 调用、bridge 入口/真实 round trip 和 Git 固定查询。计数不参加决策。默认构建计数器无效，测试用 build tag 启用；测试串行记录每次调用前后增量，不重置共享全局计数。

| 场景 | POSIX parse | bridge parse / round trip | Git 查询 |
| --- | --- | --- | --- |
| Windows 上显式 POSIX 的普通读取 | 5 | 1 / 1 | 0 |
| Windows 上显式 POSIX 的普通写/unknown | 4 | 1 / 1 | 0 |
| PowerShell 普通读写 | 0 | 2 / 2 | 0 |
| 直接 argv 的普通读写 | 0 | 0 / 0 | 0 |
| 直接 argv 的 `git push origin` | 0 | 0 / 0 | 1 |
| PowerShell 的 `git push origin` | 0 | 2 / 2 | 1 |

对于 write-like 调用，复核式再次分析当前重复相同工作量；读取不计复核。这里测量的是与当前复核条件一致的第二次分析，不模拟等待用户期间的实际配置变化，变化重审由已有集成测试覆盖。

Windows 显式 POSIX 场景是跨方言测试入口，并不是 Windows shell_run 实际执行 POSIX。`assessShellCommand` 接收显式 flavor，但 Git 辅助枚举仍按工具名/GOOS 选择 parser，所以这些样本额外调用 PowerShell。这是阶段 2 统一解析对象必须消除的方言漂移；当前阶段如实锁定，不提前修复。

## 5. 延迟和分配初测

命令：`go test ./internal/app -run '^$' -bench '^BenchmarkCommandAnalysis' -benchtime=3x -count=1`。全部 31 个 warm 子基准和 cold bridge 基准通过。fixture 创建和 warm bridge 启动不计入 warm 时间；cold 仅表示 bridge 进程重启，不代表清空操作系统文件缓存。

| 样本 | 初测耗时/op | 分配/op |
| --- | --- | --- |
| argv uniq 读取 | 0.600 ms | 14,576 B / 178 allocs |
| 显式 POSIX cat 读取 | 2.462 ms | 103,669 B / 569 allocs |
| PowerShell Get-Content | 3.845 ms | 60,176 B / 438 allocs |
| PowerShell Set-Content | 7.738 ms | 104,946 B / 714 allocs |
| argv Git 多 push URL | 52.269 ms | 187,280 B / 1,657 allocs |
| PowerShell Git 多 push URL | 57.898 ms | 178,656 B / 1,757 allocs |
| PowerShell cold bridge | 532.759 ms | 165,952 B / 821 allocs |

每项仅 3 次，属于可复现入口与量级初测，不是统计显著的性能结论。后续比较需在相同环境增加 count/benchtime，并分别报告初始分析和复核，不用该表设置脆弱的墙钟测试阈值。

## 6. 迁移依赖清单

| 当前入口/依赖 | 阶段 1–2 处理 |
| --- | --- |
| `app/request_session.go` 初始评估及写调用复核 | 保留生命周期、调用新模块；两条路径使用同一转换 |
| `app/shell_assess.go` 主流程及 argv JSON | JSON/cfg/binding 留 app，分析和目标处理迁移 |
| `app/remote_targets.go` 调用枚举与 Git argv 评估 | 使用共享解析对象/InvocationFacts；URL 展示脱敏留 app |
| `app/stable_env.go`、`posix_env.go`、`literal_target.go` | 改为显式上下文快照并迁移 |
| `approval/assessment.go`、`shell_static.go` | 迁移分析类型和入口；AccessIntent 转换留授权适配边界 |
| readonly / writable_dirs / coverage / command_families | 先行为等价搬迁，后收敛为唯一 catalog |
| shell_parse / invocations / external_paths / scalar_subst | 共享同一个解析对象；外部候选不能授权 |
| ps_bridge / ps_probe / ps_literal / readonly_ps | 保留 host 与协议行为；后续扩充节点/重定向信息 |
| `cli/main.go` 的 `approval.CloseBridge` | 随模块迁移更新 lifecycle；保留 CLI Windows 生命周期测试 |
| `app/review.go`、review summary/presentation | 转换和 UI 消费事实，不重新分类 |
| `approval/access.go`、matching/rules、tools prepared access | 不随分析器搬迁；保留策略/执行权限边界 |

测试中的私有分析 helper 和混合 `assessment.AccessIntent()` 断言需要拆分：纯分析测试迁移，审批转换断言留 approval/app。不能为了搬包删除混合目标、拒绝和零执行验证。

## 7. 安全测试索引与执行结果

基线回放不替代以下现有测试：

- `TestApprovalP1WritableReadForms`：输出操作数、Git 嵌套写。
- `TestApprovalP1UnknownEffectsCannotBorrowTargets`：未知调用借用已知/temp 范围。
- `TestApprovalP1GitPushTargets`：多个 push URL、rewrite、本地 remote/helper。
- `TestGitTargetChangeRequiresFreshReview`：审批后 Git 目标变化。
- `TestPreparedNativeWriteReapprovesChangedTarget`、`TestApprovalP1DirectoryLinkCannotExpandSavedRule`：目标变化和目录链接实际授权。
- `shell_assess_conformance_test.go`、Windows 对应文件：完整事实/目标集和字面 fallback。
- `git_static_test.go`：仓库布局和 linked worktree/common dir。
- `TestShellAssessmentPWDUsesExecutionDirectory`：cwd、环境遮蔽和真实落盘。
- `approval_entrypoint_test.go`、`command_dispatch_test.go`：单一入口、模式、人工拒绝/批准、原始载荷及执行次数。

本地已完成：

```text
go run github.com/go-task/task/v3/cmd/task@v3.51.1 check
go run github.com/go-task/task/v3/cmd/task@v3.51.1 test
go test -tags commandtrace ./internal/app ./internal/commandtrace -run '^(TestCommandAnalysis(Baseline|WorkBaseline)|TestCountersAreOptIn)$' -count=1
go test ./internal/app -run '^$' -bench '^BenchmarkCommandAnalysis' -benchtime=3x -count=1
```

初次 fixture 把 Git 路径环境变量设为空导致 `git init` 失败；已改为取消继承并由测试清理恢复，后续生成和独立回放通过。失败未被记作生产行为差异。

Linux/macOS、race、PowerShell 5.1 实际 host 尚未验证；不把 Windows 本地通过写成跨平台完成。默认不触发 provider/UAC 集成或网络推送。

## 8. 后续差分规则

1. 纯迁移阶段 effect/evidence/coverage/targets/decision 快照不变；所有差异必须解释，不能批量重录掩盖变化。
2. parser/query 工作量应在阶段 2 明确下降；保持旧结果但减少工作可以单独更新 work 快照。
3. 语义扩展先写人工正反例，再评审具体快照差异；限制样本变 allow 必须证明全部副作用/目标。
4. 自动生成的观测不是安全真值。发现现有错误放行时独立修复，不以兼容性保留漏洞。
5. 新平台用同一 corpus 运行并审查后才保存该平台快照；真实工作负载数据另建分组，不与构造样本混用分母。

## 9. 阶段 2 后续测量

2026-10-24 在独立 checkout 对比阶段 1/2 的 Linux 23 条事实，JSON 完全一致，已新增 `baseline_linux.json`。阶段 2 工作量保存为 `work_stage2_linux.json`，原 Windows 阶段 0 测量保留。详细计数、范围和未验证平台见 [阶段 2 记录](2026-10-24-command-analysis-stage2.md)，不能用阶段 0 的重复解析计数作为阶段 2 的期望。
