# 通用环境变量路径展开（General Env Path Expansion）— 设计

**日期：** 2026-08-31
**状态：** Implemented
**范围：** `internal/pathutil`（通用展开原语 + 引用扫描器）、`internal/app`（`stable_env.go` 重构、`posix_env.go` 新增、`assessCommand` 接线、`extractSecretEnvNames`）、`internal/approval/ps_bridge.ps1`（字面量赋值 AST 形状兼容修复）

## 1 背景与问题

此前已有三层「动态目标物化」机制：稳定环境变量白名单（`stableEnvPathVars`，仅 11 个机器级变量）、引擎自动变量探针（`$PROFILE`/`$HOME`）、字面量赋值传播（`$p="C:\x"; ...`）。但仍有两个缺口：

1. **任意变量不展开**：`$env:USERPROFILE\.emacs.d\init.el`（USERPROFILE 不在白名单）等路径形引用保持动态目标，无 "Always allow" 选项。
2. **裸引用残留**：静态分析把 `$env:USERPROFILE` 作为裸变量放进 `DynamicTargets`，即使提取正则已把完整路径展开进 `KnownDirs`（面板显示 "known: ..."），残留的裸引用仍使 `HasUnresolvedPaths` 为真 → 无候选规则 → 无 "A" 选项。

POSIX 侧则完全没有等价层（仅 `$HOME`/`~`）。

## 2 目标与非目标

**目标：** 分析阶段（`assessCommand`）把所有可安全静态展开的环境变量引用物化为具体目录，动态目标仅在真正无法静态确定时保留。PowerShell 与 POSIX 对齐。

**非目标：**
- 不重写命令文本本身（引号/插值语义不同，保持「收集点展开」架构）。
- `$(...)`、`@(...)`、命令替换等真运行时表达式仍保持动态。
- `process_run` 参数是字面量，不适用。
- 写命令的裸值引用（值形使用）不展开为值目录 —— 留给纠正回路；但裸引用的「路径形子sume」对读写都生效。

## 3 可靠性模型（分类时值 == 子 shell 值）

静态展开 `$env:NAME` 只有在子 shell 观察到同一值时才可靠。四个守卫共同保证：

1. **无命令内环境变异**：PowerShell 沿用 `commandMutatesPowerShellEnvironment`；POSIX 新增 `commandMutatesPOSIXEnvironment`（mvdan AST：任何 `Assign`/`ForClause`/`FuncDecl`/`LetClause`/`DeclClause`，或 `read`/`unset`/`export`/`declare`/`local`/`env` 等绑定内建命令，解析失败也 fail-closed）。POSIX 单一变量命名空间意味着**任何**赋值都会遮蔽，故整体抑制。
2. **secret 遮蔽检查**：白名单变量靠 `validateSecretEnv` 全局保留；非白名单变量靠调用点检查 —— `request_session` 把本次调用 `secret_env` 的名字（原文 + 大写）传入 `assessCommandWithEnv`，命中即跳过展开。
3. **值卫生检查**（`envValueDirLike`）：非空、绝对路径、单行、不含路径列表分隔符（PS 拒 `;`；POSIX 拒 `:`/`;`）—— `PATH` 这类多路径值永不展开。
4. **引擎自动变量探针门**：`DynamicProbe` 命令保持动态自许可语义，不展开。

## 4 引用分类与子sume（`pathutil.ExpandEnvRefs`）

扫描命令文本（跳过单引号字面量区间；PS 反引号 / POSIX 反斜杠转义跳过），对每个引用判定：

- **路径形**（紧跟 `/` 或 PS `\`）：消费尾部（字符集排除空白/引号/shell 分隔符/`$`），逐个用 `ExpandEnvPath` 展开。
- **值形（裸）**：其他边界（空白、EOC、shell 分隔符）。
- **复合**（→ 整体 fail-closed 保持动态）：尾部结束后紧跟 `$`/转义符（相邻展开拼接）；引用后紧跟引号且引号后还有路径字符（`$FOO/x"suf"` 拼接）；braced 引用后紧跟名字字符（`${FOO}suffix` 拼接）。**闭合引号后是空白/分隔符视为安全终止**（`"$FOO/x" -Tail 5` 是最常见的合法形态）。

裸引用的处理策略（`bareEnvRefDirs`）：

- 存在任一值形使用 → 整个值目录就是诚实范围（所有路径形使用都在其下）。仅读命令展开（`allowValueDirs`）；写命令保持动态。
- 全部为路径形使用 → 子sume：逐个展开文本引用，丢弃裸引用（其信息被具体路径覆盖）。读写均生效。
- 扫描器无法证明（零使用/复合）→ 保持动态（fail-closed）。

该策略封死了两个方向的漏洞：值目录过度放宽（USERPROFILE 场景不会授权整个 home）与覆盖不足（每个路径形出现都被具体展开，不会漏到自动放行的空目录意图）。

## 5 组件变化

| 组件 | 变化 |
|---|---|
| `pathutil` | `ExpandStableEnvPath` → `ExpandEnvPath`（PS `$env:NAME\tail` + POSIX `$NAME/tail`，去白名单）；新增 `EnvDirValue`、`EnvRefParts`、`ExpandEnvRefs`；`stableEnvPathVars` 职责收窄为 secret 保留名。 |
| `app/stable_env.go` | `resolveStableEnvTargets` → `resolvePowerShellEnvTargets`（路径形展开 / 裸引用策略 / secret 遮蔽）；删除前缀式 `dropSubsumedStableEnvRefs`（被扫描器子sume 取代）。 |
| `app/posix_env.go` | 新增：`resolvePOSIXEnvTargets` + `commandMutatesPOSIXEnvironment`。POSIX 动态目标都是裸 `$NAME`，只走裸引用策略。 |
| `app/shell_classify.go` | `assessCommand` 委托新 `assessCommandWithEnv(tool, command, shadowedEnv)`；物化块按 flavor 分派，裸值目录仅读命令开启。 |
| `app/request_session.go` + `review.go` | `extractSecretEnvNames(data)` 提取 `secret_env` 名（原文+大写）传入评估。 |

## 6 行为变化

- 任意路径形 `$env:NAME\...` / `$VAR/...` 引用（值满足卫生检查）不再出现在动态目标，而是变成具体目录（可存 DirAllow 规则）。
- 面板里 "known: ..." 但仍显示动态目标的矛盾消失 —— 残留裸引用被子sume。
- 已知过度近似（可接受）：裸引用在非路径上下文的值恰好是绝对路径时（如 `Write-Host $env:ProgramFiles`），审查面板会显示该目录；仅影响显示与规则宽度，不产生越权（外部目录仍需用户批准）。

## 7 顺带修复（预存 bug）

`ps_bridge.ps1` 的 `Get-OrdinaryStringAssignment` 按 `StatementBlockAst` 取 RHS，而所有现行 PowerShell（含 5.1 与 7.6.5）的赋值 RHS 都是 `CommandExpressionAst` 直接形状 —— 字面量赋值传播自 29ad162 落地以来实际从未生效。修复为同时接受 `StatementBlockAst` / `PipelineAst` / `CommandExpressionAst` 三种形状（还原为恰好一个裸表达式）。

## 8 动态读免批（2026-08-31 追加）

环境变量展开后仍会剩余两类动态读：值不满足卫生检查的引用（如 `PATH` 本身是路径列表）与真正的运行时表达式。两项策略减少其审批摩擦，均不引入 LLM 授权（LLM 仍只补全 effect，从不作为授权来源 —— 防止自证与密钥外泄）：

### 8.1 公开环境变量白名单（方案 A）

`pathutil.publicEnvPathVars`（`PATH`、`PATHEXT`、`OS`、`PROCESSOR_*`、`NUMBER_OF_PROCESSORS`）列出值本就是公开机器元数据的变量 —— 任何本地 shell 都能观察到，读取其内容不构成能力。`bareEnvRefDirs` 对读命令中名单内变量的裸引用直接丢弃（无目录可审）→ 读+无目录 → 矩阵 Allow。POSIX 仅 `PATH`（大小写敏感）。名单刻意排除任何可能携带用户数据、凭据或私有文件指针的变量。

### 8.2 DynamicReadAllow 会话级规则（方案 B）

用户显式授权的兜底规则，覆盖剩余动态读（含 `cat "$FILE"`、`Get-Content $env:SECRET_FILE_VAR` 这类运行时决定的目标）：

- `candidateRulesForIntent` 对「读 + 未解析」意图生成候选 → 审批面板出现 "A Always allow"，"Always" 行显示 `reads of runtime-resolved targets`；动态**写**仍无候选（纠正回路继续生效）。
- 按 A 保存 `dynamic_read_allow` 规则（会话级，`--continue` 恢复，无 DB 迁移 —— `rule_type` 为无枚举校验字符串）。
- `RulesAllowIntent`：规则仅在 `DominantClass == Read` 时覆盖未解析部分；具体目录组照常要求 `DirAllow`（混合意图不被整体放行）；动态写硬失败保持。
- 与 `DirAllow` 同语义地先于 `ReviewAlways` 模式门生效（saved rule 即用户明示同意）。

### 8.3 安全记录

- B 的范围（用户选择）= 所有动态读，包括未来对密钥文件路径变量的读取 —— 由用户在面板上显式按 A 授权，标签写明范围。
- 两项改动均未扩大矩阵的自动放行面：`ClassifyAccess` 不变，未获规则且不在白名单的动态读照旧询问。
