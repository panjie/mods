# 动态目标确定性物化（Dynamic-Target Materialization）— 设计

**日期：** 2026-08-28
**状态：** Approved
**范围：** `internal/approval`（探针执行器 + 资格判定 + `CommandAssessment.AssignedVariables`）、`internal/app`（`assessCommand` 接入与物化合并）

## 1 背景与问题

PowerShell 命令里大量出现运行时目标表达式（如 `Set-Content $PROFILE`、`Add-Content $PROFILE`、`$HOME` 相关路径）。静态分析无法解析这些表达式，因此：

1. 决策矩阵对任何含未解析目标的命令一律 `Ask`（`ClassifyAccess` fail-closed）。
2. `candidateRulesForIntent` 对含未解析目标的意图返回 nil —— 审批 UI **没有 "Always allow"**，同类命令每次都要问。
3. 审批摘要只显示 `runtime target $PROFILE` 表达式，客户难以判断。

`powershell_run` 每次派生全新 `-NoProfile -NonInteractive` 进程、无会话状态，因此**未被命令内赋值的引擎自动变量**在子 shell 里的值完全确定，可以在执行前解析出来。

## 2 目标与非目标

**目标：** 审批前把「引擎自动变量」类动态目标（`$PROFILE` / `$HOME` 及其四个 `$PROFILE.*` 路径属性）物化成具体绝对路径，使命令变成可审查的具体目标：矩阵照常裁决、可生成目录规则、UI 显示真实路径。

**非目标（显式排除）：**
- 不改矩阵决策、不放松任何安全边界（纯确定性解析，无语义授权）。
- 不解析命令内赋值的局部变量（`$target = ...; Remove-Item $target`），其值依赖运行时执行，仍交给现有预检纠正让模型拆分。
- 不解析 `$(...)` 子表达式、`@()`、属性链（`.PSObject` 等）。
- 本轮不解析 `$env:NAME`（非稳定名），留给后续 phase（需与请求级 env 注入/secrets 保留名机制核对，避免分类与子 shell 不一致）。→ 已由 2026-08-31 通用环境变量路径展开设计实现（调用点 secret 遮蔽检查补齐了该前提）。
- 不覆盖 POSIX。

## 3 资格规则（全部满足才探针）

1. 目标形态匹配 `(?i)^\$\{?(profile|home)\}?(?:\.(CurrentUserCurrentHost|CurrentUserAllHosts|AllUsersCurrentHost|AllUsersAllHosts))?$` —— 即裸自动变量或 `${...}` 形式，可选四个 profile 属性之一，**无字面量路径后缀**。
2. 变量**未在命令内赋值**（`CommandAssessment.AssignedVariables` 命中即排除）。
3. 命令不改环境（沿用 `commandMutatesPowerShellEnvironment` 门）。
4. `Effect != unknown` 且非 `DynamicProbe`（沿用现有 PowerShell 稳定变量解析的门条件）。
5. 值验收：探针输出必须为单行非空绝对路径，否则留在 `DynamicTargets`（fail-closed）。

## 4 探针安全

- 探针只执行 `Write-Output $PROFILE` / `Write-Output $HOME` / `Write-Output $PROFILE.<prop>`，表达式从**校验后的白名单变量名/属性名**重建，**从不**把用户命令片段送进探针 —— 无注入面。
- 一次性子进程复用执行器的宿主解析（`getWindowsShellPath`，pwsh 优先）与参数（`-NoProfile -NonInteractive -ExecutionPolicy Bypass`），保证看到的变量值与执行子 shell 一致。
- 2 秒超时；进程级缓存（按变量名+属性键，仅缓存成功结果）；非 Windows / 宿主缺失 / 解析失败 → 全部留在动态（fail-closed）。
- 值验收要求绝对路径，拒绝空值/多行/相对值。

## 5 集成点

```
assessCommand (shell_classify.go)
  ├─ AssessShellStaticWithPolicy → assessment（含 AssignedVariables）
  ├─ resolveStableEnvTargets（既有，机器级 $env: 本地展开）
  ├─ ResolveEngineAutomaticTargets(dynamic, assigned)   ← 新增，探针
  ├─ materializeProbeTargets(known, dynamic, resolved)  ← 新增，合并
  └─ finalizeCommandAssessment（partition + reviewability）
```

- `internal/approval/assessment.go`：`CommandAssessment` 新增 `AssignedVariables []string`，在 `assessPowerShellIR` 中从 IR 的 `AssignmentTargets`/`ScriptBlockAssignmentTargets` 归一化带出（小写、去 `$`、去重排序）。
- `internal/approval/ps_probe.go`（新）：`probeEligibleRef` 正则、`probeHostCommand`（可覆盖）、`probeVariableValue`（缓存）、`ResolveEngineAutomaticTargets`。
- `internal/app/shell_classify.go`：在既有 `flavor == PowerShell && (read||write) && !DynamicProbe && !mutatesEnv` 块内追加探针解析与物化合并。

物化后连锁效应：`finalizeAssessmentReviewability` 不再对已解析目标标记 `dynamic_write_target`（省掉重写往返）；矩阵按真实目录裁决；`RulesForDirs` 生成候选规则（"Always allow" 出现）；审批摘要显示具体路径。

## 6 失败模式

| 情形 | 行为 |
|---|---|
| 非 Windows / 无 pwsh/powershell | 探针返回错误，全部留在动态 |
| 值非绝对路径 / 多行 / 空 | 留在动态 |
| 命令内赋值该变量 | 排除（不探针） |
| 命令改环境 | 整块跳过（沿用既有门） |
| 探针超时 | 留在动态 |
| 含字面量后缀（如 `$PROFILE\foo`） | 不匹配正则，留在动态 |

## 7 测试

- 单元（`internal/approval/ps_probe_test.go`，可跨平台）：资格判定（形态/赋值排除/属性链拒绝）、`ResolveEngineAutomaticTargets`（以覆盖的 `probeHostCommand` 驱动）、值验收失败。
- 单元（`internal/app`）：`materializeProbeTargets` 合并逻辑（解析目标从 dynamic 移入 known、非解析目标保留）。
- 集成（`internal/app/shell_classify_windows_test.go`，Windows-only）：真实探针解析 `Set-Content $PROFILE` → `KnownDirs` 含 profile 文件路径、`DynamicTargets` 不再含 `$PROFILE`、矩阵 ask 且可生成候选规则。
- 既有测试更新：`TestAnalyzeShellCommandPowerShellProfileWriteKeepsRuntimeTargetsUnresolved`（`$PROFILE.CurrentUserCurrentHost` 现可解析，`$prof`/`$dir` 仍动态）、`TestAnalyzeShellCommandDoesNotConcreteDynamicClassifierPaths`（改用非引擎动态表达式保持原意图）。
