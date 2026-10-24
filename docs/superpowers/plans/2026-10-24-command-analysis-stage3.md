# 命令分析阶段 3：统一语义库

日期：2026-10-24。基于 `70b2737`，按用户“实施第三阶段”授权推进。代码与 Linux 验证完成，本轮修改已按用户要求纳入本次本地提交，未推送。Windows/macOS 真实执行验收待补；不将交叉编译算作运行证据。

## 实现范围

- `catalog_entries.go` 是命令身份、alias、只读子命令、写规则、完整写前提及测试引用的唯一注册入口。旧 readonly、PowerShell、subcommand 表和独立 bounded-write switch 已移除；旧 token API 仅委托。Git repository-write 子命令声明也归入 catalog，宿主目录查询继续使用阶段 2 的受限接口。
- `catalog.go` 在一次身份选择后产生 `InvocationFacts`：effect、evidence、coverage、gap、read/write/candidate 角色模型及 legacy target 投影。现有命令按 compatibility 条目保留原支持范围，不能把“迁移通过”当成外部程序全部参数已审计。
- `catalog_options.go` 为 uniq/xxd 提供通用选项绑定。保留原有 flag/value、`--`、单个 `-`、输入/输出操作数和 reverse 语义；未知选项、缺值、多余操作数及未知组合保持不完整，不新增 `--name=value` 或组合短选项支持。
- 注册校验覆盖重复 ID/身份、非规范 PowerShell 名称、别名缺失/循环/携带冲突规则、选项冲突、子命令冲突、缺少测试及自相矛盾的规则。注册过程不执行规则或宿主查询；测试引用必须对应真实测试函数。
- `semantics.go` 按 AST CallExpr / IR node ID 保存一次分析内的事实；只读、写路径、完整度、诊断、无显式信任对照及 Git 枚举共用。每次重新评估创建新 session，不跨 Analyze 缓存可变事实。嵌套 env/find/xargs 的语义求值也复用，并受深度上限约束。
- 同一调用的 read、write-discovery、coverage 参数证据强度不同：动态读取、标量替换约束与精确参数边界分别保留为一次 catalog 求值的输入。相同投影只绑定一次，不把弱路径发现升级为完整授权证据。
- 原生 process 使用 native 规则，不套 PowerShell alias；POSIX-only `cd` 不成为原生程序规则。PowerShell 中显式 `.exe/.cmd/.bat` 使用 native 语义，不能因去除后缀继承 cmdlet 的只读声明。兼容迁移不借此扩大原先未知的 PowerShell 原生读取范围。
- 显式只读信任保持独立 EvidenceTrust，不写入注册表；外围重定向、未知兄弟/嵌套调用仍独立判断。

## 独立安全修复

以下差异与机械迁移分开记录，并由 `catalog_safety_test.go` 的独立断言覆盖：

1. 旧表把 `date --set`、设置日期的数字操作数以及 `hostname 新名称` 当成读取。现在只认可明确查询语法；设置或未知形式进入普通审批。
2. `xargs uniq` 的运行时追加参数可能变成输出操作数，不能沿用 name-only read。date/hostname 的运行时参数也不能由 xargs 继承只读声明。
3. `sort.exe -o output input` 不能继承 PowerShell `sort` alias 的只读声明；`Get-Content.exe` 等不因同名 cmdlet 自动读取。原生 `sc`、`ri` 等不继承 PowerShell alias。
4. `cd` 仅作为 POSIX shell builtin 识别；直接 process、env/xargs/find 的原生子程序不因同名获得读取证据。

这些命令仍进入现有 effect/target 审批，不新增失败重试、模型分类或用户配置。

## 差分证据与验证

`testdata/catalog_legacy.json` 在独立 `70b2737` worktree 中调用旧只读、写路径、风险及 bounded-write API 采集，包含 1,032 条观测、覆盖全部 251 个注册身份。每个身份至少有空参数、双操作数、未知选项、`--` 用例；子命令族另有已知/未知子命令。临时 worktree 已清理。

该文件没有 update 开关。测试对 date/hostname 的旧错误 read 明确列出例外，其余观测保持一致。PowerShell 条目观测只证明无宿主 invocation 语义，不代替真实 parser/bridge 验证。

补充测试覆盖 uniq/xxd 的正负语法与 argv 端到端、角色记录、空输出、身份隔离、信任范围、cp/mv/未知兄弟保持 incomplete、AST/IR 每节点一次求值、20 层 env + find 子调用只求值 22 次、超深 wrapper 保守降级及注册零规则执行。

执行命令：

```sh
go build ./...
go test ./...
go test -tags commandtrace ./internal/commandanalysis ./internal/app ./internal/commandtrace
go test -race ./internal/commandanalysis ./internal/approval ./internal/app
GOOS=windows GOARCH=amd64 go test ./internal/commandanalysis -c -o /tmp/mods-commandanalysis-stage3-windows.test.exe
GOOS=windows GOARCH=amd64 go build ./...
git diff --check
```

Linux 原有 23 条事实及 stage2 work 计数无变化：初次 POSIX 解析 17 次、写复核 12 次；app 基线、Linux/Windows 事实/work 快照均未修改。继续沿用阶段 1/2 记录的 Task runner 依赖编译问题，使用 Taskfile 等价原生命令，没有为修复 Task runner 修改产品依赖。

未执行 Windows 真实 pwsh/5.1、junction/生命周期验收或 macOS；未运行 provider integration、UAC 或远端 push。

## 下一阶段边界

阶段 4 才将所有 migrated 命令的完整 read/write/candidate 目标、叶子解析模式及源/目的角色接入审批与展示。当前角色模型和 legacy 写投影并存，不宣称角色化授权完成；cp/mv、wrapper、Git hooks 等不完整写继续不复用规则。阶段 5/6 的 Git 专项与旧门面删除也未在本轮完成。
