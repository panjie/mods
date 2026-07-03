# PowerShell AST 只读命令分类器设计

- 状态：已确认（§1–§5 逐节通过）
- 日期：2026-07-03
- 范围：`internal/approval`、`internal/app`

## 1. 背景与问题

mods 当前对 `powershell_run` 工具的命令审查：`shell_classify.go:50` 的 `if tool != "powershell_run"` 跳过 POSIX AST 分类器，所有 PowerShell 命令走 Tier 2（`isSimpleReadOnly`，仅匹配 POSIX 命令名，不匹配 `Get-ChildItem`）→ Tier 3 LLM 分类器（5 秒超时）。

POSIX 分类器（`IsReadOnlyPOSIX`）已利用 `mvdan.cc/sh` AST 在本地判定管道、子命令、命令替换等只读命令。PowerShell 无 Go 原生解析器，但 `System.Management.Automation.Language.Parser::ParseInput` 提供官方 AST 解析（纯解析不执行）。

本设计通过持久化 `pwsh.exe` 桥接进程调用 PowerShell 官方解析器，在本地判定 PowerShell 命令是否只读，减少 LLM 往返。

## 2. 目标

- `Get-ChildItem`、`Get-Content file.txt`、`Test-Path x`、`Get-Process` 等常见只读 cmdlet 在本地判定，不调 LLM
- 管道中的只读 cmdlet（`Get-ChildItem | Sort-Object Name`）在本地判定
- 别名（`gci`、`ls`、`cat`、`gps`）正确识别
- **fail-closed**：任何不确定情况降级到 LLM
- **绝不执行输入命令**：桥接仅解析，不执行
- 复用现有 `extractExternalPaths` 提供 `AffectedDirs`
- 不修改 `review.go`（远端已重构，改进 `analyzeShellCommand` 后效果自动传递）

## 3. 架构

### 3.1 新文件（均在 `internal/approval/`）

| 文件 | 职责 |
|---|---|
| `ps_bridge.ps1` | 嵌入式 PowerShell 脚本 — 持久化进程，`Parser::ParseInput` 解析，输出 JSON IR |
| `ps_bridge.go` | Go 侧桥接进程管理 — 持久化 `pwsh.exe`，JSON 协议，失败重启 |
| `readonly_ps.go` | `IsReadOnlyPowerShell(command) (bool, string)` — IR 解析 + cmdlet 白名单 |
| `readonly_ps_test.go` | 单元测试（`//go:build windows` tag） |

归属 `internal/approval` 与 `readonly.go`（POSIX）并列，相同包、相同模式。

### 3.2 IR 流

```
Go: command string → JSON {"cmd":"..."} → stdin → pwsh bridge
pwsh: Parser::ParseInput → walk AST → JSON IR → stdout → Go
Go: IR → classify (risk flags + cmdlet allowlist) → (bool, string)
```

### 3.3 IR 格式

```json
{
  "version": "1",
  "commands": ["get-childitem", "sort-object"],
  "operators": ["|"],
  "redirects": [],
  "expansions": [],
  "risk_flags": [],
  "parse_errors": [],
  "has_script_block": false,
  "has_assignment": false,
  "has_background": false,
  "has_stop_parsing": false,
  "has_control_flow": false,
  "command_args": {"get-childitem": ["-Path"]}
}
```

### 3.4 集成到 `internal/app/shell_classify.go`

当前 `if tool != "powershell_run"` 改为分支：

```go
if tool == "powershell_run" {
    if ro, reason := approval.IsReadOnlyPowerShell(command); ro {
        return shellCommandAnalysis{
            NeedsReview:  false,
            AffectedDirs: externalPaths,
            Reason:       reason,
        }
    }
} else {
    if ro, reason := approval.IsReadOnlyPOSIX(command); ro {
        // ... 现有 POSIX 路径 ...
    }
}
```

Tier 2（`isSimpleReadOnly`）和 Tier 3（LLM）不变。

## 4. 桥接脚本 `ps_bridge.ps1`

### 4.1 设计

持久化进程循环：读 stdin JSON → 解析 → 输出 stdout JSON IR → 循环到 EOF。

**合约：绝不执行输入命令。** 仅 `Parser::ParseInput` 解析 + AST 遍历。

### 4.2 AST 遍历

| AST 节点 | IR 输出 |
|---|---|
| `CommandAst` | `commands[]` += 命令名（小写）；`command_args[]` += flags |
| `PipelineAst` (>1 元素) | `operators[]` += `\|` |
| `PipelineChainAst` (PS7+) | `operators[]` += `&&`/`\|\|` |
| `FileRedirectionAst` | `redirects[]` += `FileRedirection` |
| `MergingRedirectionAst` | `redirects[]` += `MergingRedirection`（安全，不写文件） |
| `SubExpressionAst` (`$(...)`) | `expansions[]` += `subshell` |
| `VariableExpressionAst`（非常量） | `expansions[]` += `var` |
| `ScriptBlockExpressionAst` (`{ ... }`) | `has_script_block = true` |
| `AssignmentStatementAst` | `has_assignment = true` |
| `IfStatementAst`/`SwitchStatementAst`/`ForStatementAst`/`ForEachStatementAst`/`WhileStatementAst`/`DoWhileStatementAst`/`TryStatementAst` | `has_control_flow = true` |
| Token `--%` | `has_stop_parsing = true` |
| Token `-EncodedCommand`/`-enc`/`-en` | `risk_flags[]` += `invoke_expression` |

### 4.3 变量排除

`$_`、`$PSItem`、`$true`、`$false`、`$null` 不标记为 `var` expansion（安全常量/迭代变量）。

### 4.4 相比 late-cli 参考实现的增强

1. `has_script_block` — 检测 `ScriptBlockExpressionAst`（late-cli 未标记）
2. `has_assignment` — 检测 `AssignmentStatementAst`
3. `has_background` — 检测后台调用操作符 `&`
4. `has_stop_parsing` — 检测 `--%` token
5. `has_control_flow` — 检测 if/switch/for/foreach/while/try

## 5. Go 侧桥接进程管理 `ps_bridge.go`

### 5.1 持久化进程

- 全局单例 `bridgeProcess`，`sync.Mutex` 保护
- 首次 `IsReadOnlyPowerShell` 调用时懒启动
- 传输失败自动重启（1 次重试）
- `CloseBridge()` 显式清理（测试用）

### 5.2 进程启动

```go
exec.Command(shell, "-NoProfile", "-NonInteractive",
    "-ExecutionPolicy", "Bypass", "-EncodedCommand", encoded)
```

- `shell`：优先 `pwsh.exe`，回退 `powershell.exe`（`exec.LookPath`）
- `-EncodedCommand`：UTF-16LE base64，避免引号转义
- `-NoProfile`：跳过 profile 加载，减少冷启动

### 5.3 JSON 协议

- 请求：`{"cmd":"..."}\n` → stdin
- 响应：一行 compact JSON IR ← stdout
- 超时：15 秒/调用
- 64KB 命令大小限制，拒绝 null 字节

### 5.4 Fail-closed

- 非 Windows 或无 `pwsh.exe` → `(false, "")`
- 任何传输/JSON/schema 错误 → `(false, "")`
- 调用方降级到 LLM

### 5.5 平台处理

- `ps_bridge.go` 无 build tag（Go 代码跨平台编译）
- 运行时检测：非 Windows 或无 `pwsh.exe` → 直接返回 `(false, "")`
- `ps_bridge.ps1` 通过 `//go:embed` 嵌入

### 5.6 孤儿进程

- 接受已知限制：Go 进程崩溃时桥接可能孤儿
- `CloseBridge()` 提供显式清理
- 未来可用 Windows Job Object 改进

## 6. 只读分类器 `readonly_ps.go`

### 6.1 分类逻辑（全部 fail-closed）

| 检查 | 条件 | 结果 |
|---|---|---|
| 解析错误 | `len(parse_errors) > 0` | 非只读 |
| 脚本块 | `has_script_block` | 非只读 |
| 赋值 | `has_assignment` | 非只读 |
| 控制流 | `has_control_flow` | 非只读 |
| 后台 | `has_background` | 非只读 |
| 停止解析 | `has_stop_parsing` | 非只读 |
| 文件重定向 | `redirects` 含 `FileRedirection` | 非只读 |
| 子表达式 | `expansions` 含 `subshell` | 非只读 |
| 变量参数 | `expansions` 含 `var` | 非只读 |
| 编码命令 | `risk_flags` 含 `invoke_expression` | 非只读 |
| 分号 | `operators` 含 `;` | 非只读 |
| `&&`/`\|\|` | `operators` 含 `&&`/`\|\|` | 非只读 |
| 管道 | `operators` 含 `\|` | 所有命令必须只读 |
| 命令白名单 | 所有 `commands` 在白名单 | 否则非只读 |

### 6.2 Cmdlet 只读白名单（含别名，小写）

| 类别 | Cmdlets (别名) |
|---|---|
| 文件系统读 | `get-childitem`(gci/ls/dir), `get-content`(gc/cat/type), `get-item`(gi), `get-itemproperty`, `get-itempropertyvalue`, `test-path`, `resolve-path`, `get-filehash`, `get-acl`, `select-string`, `get-location`(gl/pwd), `get-psdrive`, `get-psprovider`, `convert-path`, `join-path`, `split-path` |
| 对象检查 | `get-member`(gm), `get-unique`(gu), `compare-object`(compare), `join-string`, `get-random`, `convertto-json`, `convertfrom-json`, `convertto-csv`, `convertfrom-csv`, `convertto-xml`, `convertto-html`, `format-hex` |
| 管道变换 | `select-object`(select), `sort-object`(sort), `group-object`(group), `where-object`(?/where), `measure-object`(measure), `format-table`(ft), `format-list`(fl), `format-wide`(fw), `format-custom`(fc), `out-string`, `out-host`, `out-null` |
| 输出 | `write-output`(write/echo), `write-host` |
| 系统信息 | `get-process`(gps/ps), `get-service`(gsv), `get-computerinfo`, `get-host`, `get-date`(date), `get-hotfix`, `get-timezone`, `get-uptime`, `get-culture`, `get-uiculture`, `get-alias`(gal), `get-history`(h/history) |
| 其他 | `start-sleep`(sleep) |

### 6.3 刻意排除（安全陷阱）

| Cmdlet | 原因 |
|---|---|
| `get-command` | 触发模块自动加载（运行 `.psm1` init 代码） |
| `get-help` | 同上 |
| `select-xml` | XXE 外部实体解析 |
| `test-json` | `$ref` 外部 URL 获取 |
| `get-wmiobject`/`get-ciminstance` | 网络泄露（ICMP/DNS/NTLM） |
| `get-clipboard` | 暴露敏感数据 |
| `foreach-object` | 接受脚本块（已被 `has_script_block` 拦截，仍排除以明确） |
| `set-location`/`push-location`/`pop-location` | 修改 cwd |
| 所有 `set-*`/`new-*`/`remove-*`/`add-*`/`clear-*`/`copy-*`/`move-*`/`rename-*`/`invoke-*`/`start-*`/`stop-*`/`restart-*` | 写/修改操作 |

## 7. 测试策略

### 7.1 单元测试 `internal/approval/readonly_ps_test.go`

`//go:build windows` tag（需 Windows + pwsh.exe）。

**只读（期望 `true`）：**
- 简单 cmdlet：`Get-ChildItem`、`Get-Content file.txt`、`Test-Path x`、`Get-Process`、`Get-Date`
- 带参数：`Get-ChildItem -Path C:\Users -Recurse`、`Get-Content -Path file.txt -TotalCount 10`
- 管道：`Get-ChildItem | Sort-Object Name`、`Get-Process | Select-Object Name,Id`、`Get-ChildItem | Measure-Object`
- 别名：`gci`、`ls`、`cat file.txt`、`gps | select Name`
- PS3+ 简化语法：`Get-ChildItem | Where-Object Name -eq 'foo'`（无脚本块）

**非只读（期望 `false`）：**
- 脚本块：`Where-Object { $_.Name -eq 'foo' }`、`ForEach-Object { $_ }`
- 子表达式：`$(Get-Date)`、`Get-Content $(Get-ChildItem)`
- 变量参数：`Get-Content $env:SECRET`、`Get-Process $var`
- 重定向：`Get-Process > out.txt`、`Get-Date >> log.txt`
- 赋值：`$x = Get-Date`
- 控制流：`if ($true) { Get-Date }`、`foreach ($f in $files) { Get-Content $f }`
- 后台：`Get-Process &`
- 分号：`Get-Date; Get-Process`
- `&&`/`||`：`Test-Path x && Get-Content x`
- 写 cmdlet：`Set-Content file.txt "hello"`、`Remove-Item file.txt`、`New-Item -Path x`
- 排除 cmdlet：`Get-Command`、`Get-Help`、`Select-Xml`
- 停止解析：`git --% --foo`
- 编码命令：`pwsh -EncodedCommand ...`

### 7.2 非 Windows 测试

`IsReadOnlyPowerShell` 在非 Windows 或无 pwsh.exe 时返回 `(false, "")` — 可在任意平台测试（无 build tag）。

### 7.3 集成测试 `internal/app/shell_classify_test.go`

- 验证 `powershell_run` + 只读 cmdlet 不调 LLM
- 验证 `powershell_run` + 写 cmdlet 走 LLM
- 验证 `shell_run` 仍走 POSIX 路径（无回归）

### 7.4 桥接进程管理测试

- `CloseBridge()` 清理资源
- 传输失败后自动重启

## 8. 文件变更清单

| 文件 | 变更 |
|---|---|
| `internal/approval/ps_bridge.ps1` | **新建** — 嵌入式 PowerShell 桥接脚本 |
| `internal/approval/ps_bridge.go` | **新建** — Go 侧桥接进程管理 |
| `internal/approval/readonly_ps.go` | **新建** — `IsReadOnlyPowerShell` + cmdlet 白名单 |
| `internal/approval/readonly_ps_test.go` | **新建** — 单元测试（`//go:build windows`） |
| `internal/app/shell_classify.go` | **修改** — `powershell_run` 分支调用 `IsReadOnlyPowerShell` |
| `internal/app/shell_classify_test.go` | **修改** — 增加 PowerShell 集成测试 |

**不修改的文件**：
- `internal/app/review.go` — 远端已重构，改进 `analyzeShellCommand` 后效果自动传递
- `internal/approval/readonly.go` — POSIX 分类器不变

## 9. 验证命令

```bash
# Windows 上运行 PowerShell 测试
go test ./internal/approval -run TestIsReadOnlyPowerShell -count=1

# 全量 approval 测试
go test ./internal/approval -count=1

# 集成测试
go test ./internal/app -run TestAnalyzeShellCommand -count=1

# 构建检查
go run github.com/go-task/task/v3/cmd/task@v3.51.1 check

# 全量测试
go run github.com/go-task/task/v3/cmd/task@v3.51.1 test
```
