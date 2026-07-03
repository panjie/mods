# AST 只读 Shell 命令分类器设计

- 状态：已确认（§1–§5 逐节通过）
- 日期：2026-07-03
- 范围：`internal/approval`、`internal/app`

## 1. 背景与问题

mods 当前对 shell 命令的审查依赖三层管线：

1. **本地快速路径** `isSimpleReadOnly`（`shell_classify.go:308`）：一个约 25 条命令的白名单 + 拒绝任何 shell 元字符（`|`、`>`、`;`、`&&`、`$(`）。命中则判定只读，跳过 LLM。
2. **LLM 分类器** `classifyShellWithLLM`：5 秒超时，结果缓存在 LRU。
3. **后处理**：正则 `extractExternalPaths` 合并外部路径到 `AffectedDirs`。

问题：快速路径过于保守。以下常见只读命令全部落入 LLM：

- **管道**：`cat file | grep foo`、`ls -la | head -5`（元字符 `|` 触发降级）
- **子命令**：`git status`、`docker ps`、`kubectl get pods`（不在白名单）
- **外部路径只读**：`cat /etc/passwd`（白名单命中但 `extractExternalPaths` 非空 → 降级 LLM 做路径分析）
- **命令替换**：`echo $(date)`（元字符 `$(` 触发降级）

每次 LLM 调用增加约 1–5 秒延迟。本设计利用已有的 `mvdan.cc/sh` AST 解析器（`go.mod` 已依赖，`internal/approval/shell_parse.go` 和 `writable_dirs.go` 已在使用）构建一个结构化的只读分类器，在本地确定性判定更多命令，减少 LLM 往返。

## 2. 目标

- 管道、`&&`/`||`、子 shell、命令替换中的只读命令在本地判定，不调 LLM。
- `git status`、`docker ps`、`kubectl get` 等常见开发子命令在本地判定。
- 只读命令 + 外部路径（如 `cat /etc/passwd`）在本地判定只读，`AffectedDirs` 由现有正则提供，审批矩阵决定是否 Ask——不调 LLM。
- **不引入新依赖**：复用 `mvdan.cc/sh/v3`。
- **不改变安全姿态**：任何不确定情况 fail-closed 降级到 LLM。
- **不改变 PowerShell 路径**：`powershell_run` 继续走现有 simple tokenizer + LLM。

## 3. 架构

### 3.1 新文件 `internal/approval/readonly.go`

归属 `internal/approval`（纯逻辑，无 LLM 依赖，可独立单测），与 `shell_parse.go`、`writable_dirs.go` 并列。

```go
// IsReadOnlyPOSIX 使用 mvdan.cc/sh AST 分析 POSIX shell 命令，
// 确定性判定其是否只读。返回 (true, reason) 表示只读；
// (false, "") 表示非只读或无法确定（fail-closed，调用方降级到 LLM）。
func IsReadOnlyPOSIX(command string) (bool, string)
```

### 3.2 复用已有代码

| 已有函数 | 文件 | 用途 |
|---|---|---|
| `syntax.NewParser(syntax.Variant(syntax.LangPOSIX))` | `shell_parse.go` | POSIX 解析器 |
| `staticShellWord(*syntax.Word) (string, bool)` | `shell_parse.go:158` | 从 Word 提取静态字符串 |
| `redirectionWrites(syntax.RedirOperator) bool` | `shell_parse.go:193` | 判断重定向是否写入 |

### 3.3 集成到 `internal/app/shell_classify.go`

`analyzeShellCommand` 的快速路径从 2 层变为 3 层：

```
0. shellAnalyzer 测试缝（不变，最先检查）
1. 提取外部路径（一次提取，所有层复用）
2. Tier 1: AST 分类器（跳过 powershell_run）
   → IsReadOnlyPOSIX(command) 返回 (true, reason)?
   → 返回 {NeedsReview: false, AffectedDirs: externalPaths, Reason}
   → 覆盖两种情况：无外部路径（自动放行）和有外部路径（审批矩阵 Ask）
3. Tier 2: 现有 isSimpleReadOnly（不变，降级兜底）
   → 处理 cmd.exe 语法（mvdan 解析失败）的简单情况
4. Tier 3: LLM 分类器（不变）
5. 后处理：合并外部路径（不变）
```

**调用链（远端最新）**：`request_session.go` 在调用 `requestApproval` 前通过 `buildAccessIntent` 预计算 `AccessIntent`，其中调用 `analyzeShellCommand`。`requestApproval` 接收预计算的 `intent`（`reviewerDeps.accessIntent`），不再在内部重新调用 `analyzeShellCommand`。因此改进 `analyzeShellCommand` 后，效果自动传递到整个审批链——无需修改 `review.go`。

### 3.4 关键改进：只读 + 外部路径跳过 LLM

当前快速路径要求 `len(extractExternalPaths(command, ws)) == 0` 才返回只读。`cat /etc/passwd`（只读 + 外部路径）因此降级到 LLM。新设计中，AST 分类器在本地判定只读，`extractExternalPaths` 正则提供 `AffectedDirs`，审批矩阵处理"外部读 → Ask"决策——**无需 LLM 调用**。

**远端增强**：`extractExternalPaths` 现在通过 `expandCurrentUserHomePath` 将 `~` 和 `~/` 展开为 home 绝对路径（如 `~/Downloads` → `/home/user/Downloads`），使 tier 1 返回的 `AffectedDirs` 更准确，审批矩阵能正确识别外部 home 路径访问。

### 3.5 平台处理

| 工具 | 平台 | AST 分类器？ | 原因 |
|---|---|---|---|
| `shell_run` | macOS/Linux | 是 | `sh -c`，POSIX 语法 |
| `shell_run` | Windows | 是（尝试，fail-closed） | `cmd /D /C`——mvdan 对 cmd.exe 语法解析失败 → 降级到 tier 2/3。简单 POSIX 兼容命令（`echo hello`）可解析。 |
| `powershell_run` | Windows | 否 | 语法完全不同，走 tier 2/3。 |

判断条件为 `tool != "powershell_run"`（不使用 `shellToolUsesPOSIX`），因为即使在 Windows 上也想尝试 mvdan 解析简单 POSIX 兼容命令。

### 3.6 不变的部分

- `shellAnalyzer` 测试缝（在 AST 之前检查）
- `isSimpleReadOnly`（保留为 tier 2 兜底）
- `classifyShellWithLLM` 和 LRU 缓存（tier 3）
- `extractExternalPaths` 正则（复用，不替换）
- 后处理合并外部路径到 LLM 结果

## 4. AST 只读分析逻辑

### 4.1 语句级检查 `stmtIsReadOnly`

| 条件 | 结果 | 原因 |
|---|---|---|
| `stmt.Background`（尾部 `&`） | 非只读 | 后台执行不可预测 |
| 任何输出重定向（`>`、`>>`、`>&`、`&>`、`<>`、`>>\|`） | 非只读 | 复用 `redirectionWrites()` |
| 输入重定向（`<`、`<<`） | 只读 | Heredoc/重定向输入不写入 |
| 重定向目标 Word 含 `ProcSubst` | 非只读 | `>(cmd)` 在重定向目标中 |

### 4.2 命令级检查 `cmdIsReadOnly`

| AST 类型 | 规则 |
|---|---|
| `*BinaryCmd`（`\|`、`&&`、`\|\|`、`\|&`） | `X` 和 `Y` 都必须只读 |
| `*Subshell`（`(cmds)`） | 所有内部 `Stmts` 都必须只读 |
| `*CallExpr`（`cmd args`） | 叶子分类（见 §4.3） |
| 其他（`IfClause`、`WhileClause`、`ForClause`、`CaseClause`、`Block`、`FuncDecl`、`DeclClause`、`ArithmCmd`、`TestClause`、`TimeClause`） | 非只读（fail-closed） |

### 4.3 叶子命令分类 `callIsReadOnly`

1. **检查所有参数 Word Parts**：
   - `CmdSubst`（`$(cmd)`）→ 递归：所有内部语句必须只读
   - `ProcSubst`（`<(cmd)`、`>(cmd)`）→ fail-closed
   - `ParamExp`（`$VAR`、`${VAR}`）→ 只读（变量展开无副作用）；但需检查 `Repl` 和 `Exp` 字段中嵌套的 `CmdSubst`/`ProcSubst`
   - `ArithmExp`（`$((...))`）→ 只读
   - `Lit`、`SglQuoted`、`DblQuoted`、`ExtGlob`、`BraceExp` → 只读

2. **提取命令名**：从 `Args[0]` 用 `staticShellWord()` 提取。若动态 → fail-closed。

3. **分类**：
   - 命令在 `readOnlyCommands` 白名单 → 只读
   - 命令在 `subcommandReadOnly` 表 → 提取子命令（跳过前导 flag），查表
   - 未知 → fail-closed（降级 LLM）

### 4.4 核心原则：fail-closed

任何解析错误、未知命令、未知子命令、动态命令名、未处理的 AST 节点 → 返回 `false`。调用方降级到 LLM。维持现有安全姿态。

## 5. 命令分类表

### 5.1 平坦只读白名单 `readOnlyCommands`

所有命令**无论 flag 如何都是只读**（不存在写 flag）：

**现有（保留）**：`ls`、`cat`、`head`、`tail`、`wc`、`file`、`stat`、`pwd`、`echo`、`date`、`whoami`、`hostname`、`uname`、`du`、`df`、`which`、`env`、`printenv`、`basename`、`dirname`、`realpath`、`readlink`、`grep`、`egrep`、`fgrep`

**新增**：`diff`、`uniq`、`comm`、`tr`、`cut`、`strings`、`xxd`、`od`、`hexdump`、`nm`、`objdump`、`readelf`、`md5sum`、`sha1sum`、`sha256sum`、`sha512sum`、`shasum`、`cksum`、`test`、`[`、`true`、`false`、`seq`、`printf`、`id`、`groups`、`lsof`、`ps`、`free`、`uptime`、`w`、`column`、`paste`、`expand`、`unexpand`、`nl`、`rev`、`tac`、`fold`、`fmt`、`join`

**刻意排除**（带写 flag，走 LLM）：`sort`（`-o`）、`sed`（`-i`）、`find`（`-delete`/`-exec`）、`curl`/`wget`（`-o`）、`tar`（`xf`）、`dd`、`tee`、`xargs`、`awk`、`perl`、`python`、`node`、`ruby`、`make`、`cmake`

### 5.2 子命令只读表 `subcommandReadOnly`

| 工具 | 只读子命令 |
|---|---|
| `git` | `status`、`log`、`diff`、`show`、`blame`、`annotate`、`rev-parse`、`describe`、`reflog`、`shortlog`、`ls-files`、`ls-tree`、`ls-remote`、`whatchanged`、`cat-file`、`rev-list`、`merge-base`、`name-rev`、`var`、`for-each-ref` |
| `docker` | `ps`、`images`、`logs`、`inspect`、`stats`、`top`、`history`、`search`、`version`、`info`、`events` |
| `kubectl` | `get`、`describe`、`logs`、`explain`、`top`、`version`、`api-resources`、`api-versions`、`cluster-info`、`diff` |
| `go` | `version`、`list`、`vet`、`doc`、`help` |
| `npm`、`pnpm`、`yarn` | `list`、`ls`、`outdated`、`info`、`view`、`root`、`help`、`why`、`explain` |

**子命令提取**：对有子命令的工具，检查 `args[1]`（跳过前导 flag 如 `git --git-dir=x status`）。若 `args[1]` 动态或全部为 flag → fail-closed。

**刻意排除的子命令**（无 flag 分析则读写歧义）：`git branch`（创建 vs 列出）、`git config`（get vs set）、`git tag`（列出 vs 创建）、`git remote`（列出 vs 添加）、`docker network/volume`（ls vs create/rm）、`kubectl config`（view vs set）、`go env`（print vs `-w`）、`go test`（写测试二进制）

### 5.3 简化说明

初始实现**不跳过子命令前的全局 flag**（如 `git --git-dir=/x status` → fail-closed → LLM）。这是有意的——常见情况（`git status`）正常工作，罕见全局 flag 情况由 LLM 缓存。

## 6. 测试策略

### 6.1 单元测试 `internal/approval/readonly_test.go`

针对 `IsReadOnlyPOSIX` 按 AST 构造组织：

**只读（期望 `true`）**：
- 简单白名单：`ls`、`ls -la`、`cat file`、`diff a b`、`tr a-z A-Z`、`printf "hello"`
- 管道：`cat file | grep foo`、`ls -la | head -5`、`echo hello | tr a-z A-Z`
- 二元（`&&`/`||`）：`git status && git diff`、`true || false`、`test -f x && echo y`
- 子命令：`git status`、`git log --oneline`、`docker ps -a`、`kubectl get pods`、`go version`、`npm list`
- 子 shell：`(git status)`、`(ls -la; cat file)`
- 命令替换：`echo $(date)`、`echo $(git status)`、`cat $(git ls-files)`
- 输入重定向：`tr a-z A-Z < input`
- 参数展开：`echo $VAR`、`cat ${FILE}`
- 多语句：`git status; git diff`
- 全路径：`/bin/ls`、`/usr/bin/cat file`

**非只读（期望 `false`）**：
- 输出重定向：`echo hello > file.txt`、`ls >> out.log`、`cmd &> all`
- 含写命令的管道：`cat file | tee output`
- 未知命令：`rm file`、`find . -name '*.go'`、`sort file`、`make`
- 写子命令：`git push`、`git commit -m msg`、`docker run img`
- 含写的命令替换：`echo $(rm file)`
- 进程替换：`echo hello > >(cmd)`、`diff <(cmd1) <(cmd2)`
- 控制流：`if true; then ls; fi`、`for f in *.go; do echo $f; done`
- 后台：`ls &`
- 动态命令名：`$CMD file`（ParamExp 作为命令）
- 解析错误：空字符串、cmd.exe 语法（`%PATH%`、`dir /b`）
- 无子命令的工具：`git`（裸）、`docker`（裸）
- 子命令前全局 flag：`git --git-dir=/x status`（args[1] 以 `-` 开头 → fail-closed）

### 6.2 集成测试 `internal/app/shell_classify_test.go`

- 验证 `analyzeShellCommand` 对管道只读命令返回 `NeedsReview: false` 且不调 LLM
- 验证只读 + 外部路径（如 `cat /etc/passwd`）的 `AffectedDirs` 正确填充
- 验证 `powershell_run` 跳过 AST 分类器
- 验证 cmd.exe 语法降级到 `isSimpleReadOnly` 再到 LLM

### 6.3 现有测试保持绿色

`TestIsSimpleReadOnly`、`TestExtractExternalPaths`、`TestMentionsExternalPath`——全部不变（tier 2 和正则提取保留原样）。远端新增的 `expandCurrentUserHomePath` 相关测试也保持不变。

## 7. 文件变更清单

| 文件 | 变更 |
|---|---|
| `internal/approval/readonly.go` | **新建** — `IsReadOnlyPOSIX` + `readOnlyCommands` + `subcommandReadOnly` + AST 遍历 |
| `internal/approval/readonly_test.go` | **新建** — 单元测试 |
| `internal/app/shell_classify.go` | **修改** — `analyzeShellCommand` 增加 tier 1 AST 分类器 |
| `internal/app/shell_classify_test.go` | **修改** — 增加集成测试用例 |

**不修改的文件**（远端 `4bdd0ed` 已重构，无需改动）：
- `internal/app/review.go` — `requestApproval` 已改为接收预计算 `intent`，改进 `analyzeShellCommand` 后效果自动传递
- `internal/app/review_summary.go` — 已支持 `formatReviewSummaryWithIntent`，无需改动
- `internal/app/request_session.go` — `buildAccessIntent` 调用链不变

## 8. 验证命令

```bash
go test ./internal/approval -run TestIsReadOnlyPOSIX -count=1
go test ./internal/app -run TestAnalyzeShellCommand -count=1
go run github.com/go-task/task/v3/cmd/task@v3.51.1 check
go run github.com/go-task/task/v3/cmd/task@v3.51.1 test
```
