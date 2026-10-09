# Git HTTPS 认证交互桥接实施计划

日期：2026-10-09
状态：已实现，本地验证通过；Windows/Linux 实机验证待 CI。见文末实施记录。
目标：工具执行 Git 命令时，如果现有凭据不足，由 mods 接收 Git 的认证请求，向本地用户安全询问，并让原命令继续。

## 1. 首版范围与用户体验

首版覆盖 Git HTTP(S) 的用户名、密码和作为密码使用的 token 输入；主要面向 HTTPS 私有仓库。覆盖 `process_run`、POSIX `shell_run`、Windows `powershell_run`，支持 macOS、Linux、Windows。HTTP 仅用于兼容 Git 自身行为及本地测试，不主动引导用户通过明文 HTTP 提交真实凭据。

正常流程：

1. 模型发起已有的命令工具调用，例如 `git fetch` 或 `git push`。
2. mods 按现有流程评估读写目标并完成必要的操作审批。
3. Git 优先使用已有 credential helper；凭据够用时不出现 mods 认证面板。
4. Git 请求输入时，mods 展示 `Git authentication` 面板，说明请求来源、命令和所需字段。
5. 用户名使用普通输入，密码/token 使用掩码输入；无法可靠判断字段时默认掩码输入。
6. 输入经本地 IPC 直接返回 askpass helper，再由其 stdout 返回 Git。模型只接收原工具最终的正常输出或经过脱敏的错误。
7. 用户取消、命令超时或任务结束时，关闭弹窗和 broker，停止本次 mods 认证交互。

不新增模型可见的 `git_login` 工具，不新增配置键、CLI 参数或数据库表。认证输入不创建 `DirAllow`/`RemoteAllow` 规则，不扩大原命令权限。

以下留待后续独立设计：SSH 私钥解锁、SSH host key 确认、`gh auth login` 的交互菜单/设备码流程、mods 自己管理 OAuth、通用 PTY/交互式终端、凭据管理页面。用户已有的 SSH agent、外部 OAuth credential helper 和 keychain 继续按原配置运行，但不保证其自有交互能进入 mods UI。

## 2. 已确认的代码基础

| 位置 | 现状与本次用途 |
| --- | --- |
| `internal/tools/sudo_unix.go` | 已有 Unix socket、私有 helper 和随机 token 的 sudo askpass；借鉴其协议和清理模式，首版避免顺带重构 sudo |
| `internal/tools/sudo_windows.go` | sudo bridge 的 Windows 实现是空实现，Git 支持不能依赖它 |
| `main.go` | 已在正常 CLI 初始化前处理 sudo helper；新增 Git 私有 helper 分派 |
| `internal/tools/command_runner.go` | 统一进程管理、输出收集、进程树和超时；认证资源必须绑定这里的实际执行上下文 |
| `internal/tools/shell.go`、`process.go` | 命令入口和环境构建；需要统一注入 Git 执行环境 |
| `internal/tools/user_input.go`、`internal/tooling/tools.go` | 交互回调和工具配置的传递位置 |
| `internal/app/tool_support.go` | 仅在用户输入可用时注册交互回调 |
| `internal/app/user_input.go` | 现有输入 UI；`available()` 排除非 TTY、raw、minimal |
| `internal/secrets/store.go` | 有秘密引用和输出脱敏；新增仅供脱敏的登记能力，避免为运行时认证生成可复用的工具凭据引用 |
| `internal/app/request_session.go`、`shell_progress.go` | 工具输出、错误、调试信息和进度的脱敏路径 |

两个不能直接沿用的细节：

- 当前 sudo broker 使用外层上下文，而命令超时由 runner 内部创建；Git bridge 必须绑定 runner 的上下文，确保超时同步终止输入和 IPC。
- 当前 `handleSudoPrompt` 直接返回输入，不经过 `secrets.Put`。Git 需要显式登记脱敏值，不能假定掩码 UI 就等于输出脱敏。

## 3. 必须锁定的行为约定

### 3.1 现有认证配置优先

Git 的输入来源优先级为 `GIT_ASKPASS` → `core.askPass` → `SSH_ASKPASS` → 终端，credential helper 还能提供缓存或托管凭据。不得简单设置 `GIT_ASKPASS=mods-helper` 后忽略其余来源。

最终方案：仅在未设置 `SSH_ASKPASS` 时把 mods helper 安装在此最低优先级入口（显式空值也保留）。让 Git 原生处理 credential helper → `GIT_ASKPASS` → `core.askPass` → `SSH_ASKPASS` 的优先级，不重新解析或委托配置。用户命令内部显式修改认证环境时，尊重其选择，不改写脚本。

真实 Git 测试已验证 `git -C` 的仓库配置、`git -c core.askPass=...`、各环境入口、helper 缓存命中及配置失败不回退。子模块配置仍由 Git 自己读取；本次没有额外编写子模块专用集成夹具。`SSH_ASKPASS` 也会被 SSH 使用，因此 mods helper 拒绝不带 HTTP(S) URL 的请求，不支持私钥解锁或 host-key 确认。标准 Git for Windows 通过其自带 shell 启动带 shebang 的 helper；变量保存原始路径，不能把路径作为 shell 字符串加引号。

不清空或替换 `credential.helper`，不写全局或仓库 Git 配置，不运行 `git credential approve` 额外保存凭据。Git 本身可能按用户已有 helper 配置保存认证成功后的凭据；界面和文档不能承诺“绝不落盘”，只能承诺 mods 不额外持久化。

### 3.2 非交互与超时

- mods 托管的 Git 执行环境设置 `GIT_TERMINAL_PROMPT=0`，禁止缺少凭据时回落到不可用的终端。
- raw、minimal、非 TTY 和 write-target discovery 不注册认证 UI 回调。现成凭据仍可使用；需要 mods 输入时立即返回可识别错误。
- `GIT_TERMINAL_PROMPT=0` 不会禁止外部 credential helper 自己开浏览器或等待输入。此类程序仍受命令总超时约束，不宣称能统一接管其交互。
- 首版保留现有超时含义：用户输入时间计入 `timeout_ms`/配置的命令总超时，默认 60 秒。面板说明剩余时间或超时约束；不引入暂停计时的隐藏行为。
- 超时必须撤销排队或已显示的输入请求。用请求 ID 防止迟到消息关闭后续请求；取消当前认证后禁止同一次命令继续反复弹窗。
- mods 不自动重跑整条命令。Git 自身请求下一个字段可以继续，失败后如何重试由正常工具流程决定。

### 3.3 秘密与本地 IPC

- 每次工具执行创建独立 broker，使用随机认证 token；token 和 IPC 连接只在该次执行有效。
- Unix 使用受限目录中的 Unix socket；Windows 使用仅当前用户可访问的 named pipe。先检查仓库现有依赖，确有需要时再引入维护中的 named-pipe 库。
- helper 文件不包含用户密码/token；秘密不放在命令参数、URL、环境变量或临时文件中。broker 的会话鉴权 token 与 Git 凭据是不同概念。
- helper stdout 只用于向 Git 返回单行输入，不能混入日志；helper 错误不包含用户回答。
- 协议限制消息大小、字段长度、读取时间和并发请求；同一工具调用的 UI 输入串行处理，取消后关闭活动连接。
- prompt 是外部程序提供的文本：去控制字符、限制长度、脱敏 URL userinfo/query。显示的地址标记为“Git 请求的地址”，不能当作已验证的服务端身份。
- 第一次回传秘密前完成脱敏登记，覆盖模型结果、错误、进度、debug 和持久化对话输出。仅匹配原文的脱敏不能保证覆盖任意编码/变换；不得把它描述为对子进程行为的完整隔离。
- 脱敏值保留到现有会话 secrets 清理边界，确保延迟输出仍可过滤；broker、连接和 helper 则在命令结束后立即释放。
- 不把同一用户下的任意恶意进程或工具子进程排除在威胁模型之外作过度承诺；bridge 提供受限生命周期的输入通道，不新增 OS 沙箱。

## 4. 实施任务与依赖

### T0：验证 Git 兼容性，冻结注入策略

产出：真实 Git 的本地测试夹具、环境优先级行为表、Windows helper 启动验证，以及本计划第 3.1 节的最终方案。

- 用隔离 HOME/Git config 和虚拟 credential helper 验证缓存命中、用户名缺失、密码缺失及 askpass 优先级。
- 验证 `-C`、`-c`、命令环境覆盖、脚本内部调用 Git，以及执行路径含空格/非 ASCII 字符。
- 明确非 Git 命令是否需要懒创建 broker。统一在执行层准备环境，但只有真正收到 askpass 请求才进入 UI；不能只匹配命令文本中的 `git`，也不做全局 PATH 劫持。
- 验证私有 helper 入口在 POSIX 与 Git for Windows 的实际启动方式。禁止依赖未经测试的 shell quoting 或假定 `.cmd`、`.exe` 行为相同。
- 如上述目标存在不兼容，先记录受影响场景和最小修正方案；不能把 Windows 支持留成无提示空实现。

完成条件：已有认证来源保持优先，Windows 启动方式和配置传播有测试证据，后续接线不再依赖猜测。

### T1：Git askpass 协议与跨平台 helper

预计文件：`internal/tools/git_auth.go`、`git_askpass.go`、平台分文件及对应测试，`main.go`。

- 定义独立的 `GitAuthRequest` 和回调，携带 prompt、原工具命令显示信息、字段类型及执行标识；避免把 Git UI 做成 sudo UI 的别名。
- 实现独立 broker、鉴权、请求上限、连接 deadline、取消和幂等清理。
- `main.go` 在正常 CLI/配置/DB 初始化前分派私有 helper，失败返回非零状态；普通启动不受影响。
- 实现平台 transport/launcher，测试路径引用与 helper 返回值准确性。

完成条件：helper 能从假 UI 获取一个值并只向 stdout 输出该值；无 UI、错误 token、超长/损坏消息、断连、取消均有界退出且无秘密日志。

### T2：接入命令执行生命周期

预计文件：`command_runner.go`、`shell.go`、`process.go`、`tools/user_input.go`、`tooling/tools.go`、`app/tool_support.go`。

- 在 runner 创建真实 run context 后、启动进程前，增加可返回错误和 cleanup 的执行准备阶段，或等价的最小接口。准备失败时不能启动命令。
- 所有退出分支统一清理认证资源；进程启动失败、超时和父上下文取消都覆盖。
- 将 `GitAuth` 回调经 `InteractionHandlers` 传入 process/shell 配置；Windows 的 PowerShell 注册分支也必须接线。
- 合并继承环境、已解析的 `secret_env`、sudo 环境、Git bridge 环境；按平台规则替换键，避免重复变量造成生效顺序不确定。
- 保留原命令/argv/cwd，不改写脚本，不修改 shell 分类、审批规则和远端目标推断。
- 确保只读 discovery 也采用不可弹窗的执行策略，不能意外持有 UI 回调。

完成条件：三个命令入口和脚本内 Git 调用符合 T0 的支持范围；sudo 回归通过；未出现认证请求时没有额外用户交互。

### T3：认证 UI、请求撤销与脱敏

预计文件：`app/git_auth.go`、`app/user_input.go`、必要的消息定义/Update 分支、`internal/secrets/store.go`。

- 新增 Git 认证面板和 `handleGitAuthPrompt`，复用现有 text/secret 编辑器与输入队列。
- 展示清理后的命令、请求地址、字段类型和超时约束；不硬编码英文 prompt 才能工作，未知 prompt 使用保守的秘密输入。
- 用户名与密码按 Git 实际请求逐次收集，不预先要求用户提供 token。
- 新增仅供 Redact 使用的秘密登记接口，不产生可供模型引用的 secret_ref；保持既有 Resolve 的绑定规则。
- 为输入请求增加可定位的取消事件，清理排队/显示/迟到状态；避免命令已退出而弹窗留在屏幕上。
- 认证取消返回固定、可识别且无凭据的错误；取消后该命令不再弹出第二次认证请求。

完成条件：输入不进入模型上下文；取消/超时立即关闭正确的面板；后续普通问题和 sudo 输入仍正常。

### T4：行为与故障回归测试

测试先覆盖关键失败路径，再实现或修正；不为文案或私有函数结构编写脆弱镜像测试。

| 测试组 | 必须覆盖 |
| --- | --- |
| 现有认证 | helper 命中零弹窗；各 askpass 来源优先级；现有 helper 失败不被 mods 静默替换 |
| Git 输入 | username/password 两阶段、只有 password、token、未知/本地化 prompt、特殊字符 |
| 执行入口 | process、POSIX shell、PowerShell；脚本调用、`-C`、`-c`、空格路径；环境覆盖 |
| 生命周期 | 启动失败、用户取消、等待输入时超时、会话结束、helper 崩溃、活动连接退出、并发调用隔离 |
| 输入模式 | TTY 正常；raw/minimal/非 TTY/discovery 无 UI；已有凭据仍可成功 |
| 保密 | stdout/stderr/进度/debug/最终 tool result/对话保存不包含提交的秘密；helper 文件不含凭据 |
| 授权 | 认证不新增允许规则；需要审批的写仍经过 requestApproval；读取策略保持现状 |
| 兼容回归 | sudo、普通 request_user_input、secret_env、命令超时、进程树清理 |

两层测试：

1. 无网络单元/子进程测试：假 helper、假 Git、受控 IPC，覆盖大多数故障路径。
2. 真实 Git 的本地集成测试：隔离配置，使用 `git credential fill`（只用于测试，不用于产品日志）及本地受控认证服务验证挑战后输入和原命令继续。仅使用虚拟用户名/秘密，无远端账号或 provider key；需要 Git/PowerShell 的测试在对应 CI 环境明确运行，不能静默跳过所有真实路径。

涉及网络协议的夹具仅监听 loopback，测试生命周期内关闭，不更改产品的私网访问设置。完整 clone/fetch/push 协议冒烟可使用临时 bare repo 和本地 Git HTTP backend；若 CI 缺少 backend，必须记录实际覆盖范围，而不把 `credential fill` 等同于完整 push 成功验证。

### T5：文档、模型引导和最终验证

- README 说明支持 HTTPS 认证输入、推荐沿用现有 credential helper、raw/minimal 限制、超时、取消、外部 helper 和 SSH 的边界。
- `internal/tools/guidance.go` 和 `internal/prompts/identity.md` 仅添加必要行为政策：正常执行 Git，让运行时处理认证；不把 token 写进 URL/命令，不自行编造保存凭据的脚本，不自动重复失败的写操作。
- 不新增工具目录项；确认 `--list-tools`、配置覆盖测试和帮助目录未被意外改动。
- 更新本计划中的实施状态、已验证平台、明确限制及最终文件位置。

按改动运行定向测试，再执行仓库基线：

```sh
go test ./internal/tools ./internal/app ./internal/tooling ./internal/secrets ./internal/prompts -count=1
go test -race ./internal/tools ./internal/app ./internal/secrets -run 'TestGitAuth|TestGitAskpass|TestUserInput' -count=1
go run github.com/go-task/task/v3/cmd/task@v3.51.1 check
go run github.com/go-task/task/v3/cmd/task@v3.51.1 test
```

测试名按实现落地后调整，确保 race 命令实际执行了新测试。CI 在 Ubuntu/macOS/Windows 运行对应平台测试；本地有 golangci-lint 时运行 `golangci-lint run ./...`。仅修改计划时不运行上述代码测试。

人工验收使用测试仓库：缓存凭据零打扰；首次 HTTPS 认证输入后原命令继续；取消无残留 UI；超时能继续下一次对话；Windows 空格路径可用；检查 debug 和对话记录无凭据。真实外部 push 仅在后续实施任务明确授权的测试目标上进行。

## 5. 交付顺序与完成定义

依赖：T0 → T1 → T2 → T3 → T4 → T5。T1–T3 各自携带对应定向测试，T4 补齐跨层故障和兼容性场景。

建议拆成三个可审查提交：协议与平台 helper；执行生命周期/UI/脱敏接线；集成测试与文档。发布应等待整条链路及平台验证完成，不把仅有协议而无法交互的中间状态作为功能交付。

完成必须同时满足：

- 支持范围内的 Git 认证在同一次工具调用中完成，原命令继续。
- 已有凭据和 askpass 配置保持可用，无需重新登录。
- 秘密经独立通道传给 Git，模型结果和 mods 持久化记录经过脱敏。
- 不可交互、取消、超时和进程失败都有确定退出路径，无残留窗口、连接和 helper。
- Git 认证与操作审批保持分离，既有权限规则和 sudo 行为无回归。
- 三个平台的实际验证情况如实记录，仓库基线通过。

主要风险是 Git 配置优先级和 Windows launcher/IPC，其次是超时撤销输入与脱敏生命周期。配置优先级已通过 Git 自身的回退顺序解决；Windows 实现有对应平台测试及交叉编译验证，实际运行仍须由 Windows CI 确认。

## 6. 实施记录

- T0：采用最低优先级 `SSH_ASKPASS` 回退，省去原先设想的配置查询/委托；显式已有 askpass（含空值）不覆盖。
- T1：`internal/tools/git_auth*.go` 实现独立 broker、32 字节随机 token、消息大小限制、连接 deadline、取消锁止和幂等清理。Unix 使用私有目录 socket，Windows 使用仅当前用户 ACL 的 named pipe（`github.com/Microsoft/go-winio v0.6.2`）。helper 为无凭据的短脚本，进入 `main.go` 私有分派。
- T2：`commandRunner.Prepare` 在实际执行上下文内准备认证环境。process/shell/PowerShell 经配置回调接入；环境按平台键规则合并。write-target discovery 通过上下文显式禁用认证 UI，避免复用正常 registry 时意外弹窗。
- T3：`app/git_auth.go` 复用现有编辑器；用户名普通输入，未知字段掩码输入。输入请求使用独立响应 channel 作身份标识，取消消息仅关闭对应请求。请求结束清空编辑器；`secrets.RememberForRedaction` 在返回子进程前登记输入，覆盖原文及 Go JSON 转义表示，不产生工具 secret_ref。
- T4：真实 Git 测试覆盖认证优先级、路径空格/Unicode/单引号、取消锁止、超时清理、非交互。隔离的本地 TLS Git HTTP backend 跑通 push → clone → fetch，并覆盖平台脚本调用和 discovery 禁止输入。无真实账号或外部写入。
- T5：README 和运行时引导已更新，不增加公开工具、配置或权限规则。

验证记录（macOS）：

- 定向 tools/app/tooling/secrets/prompts 测试通过。
- 新认证流程及输入管理的 race 测试通过；补充的 IPC 故障和工具结果/debug 脱敏测试亦通过 race 检查。
- `go run github.com/go-task/task/v3/cmd/task@v3.51.1 check` 通过。
- `go run github.com/go-task/task/v3/cmd/task@v3.51.1 test` 全部通过。
- `GOOS=windows GOARCH=amd64 go build ./...` 通过；tools/app/secrets 的 Windows 测试二进制也完成交叉编译。编译时使用 `-exec=true` 跳过运行，这不等于 Windows 实际测试通过。
- `git diff --check` 通过。当前没有安装 golangci-lint，未运行其检查。

CI 已有 Ubuntu/macOS/Windows 全量测试矩阵，新测试会随之运行，Windows 对应测试还覆盖 PowerShell 与 named pipe。以上为提交前的本地验证记录，不包含远端 CI 结果。暂无人工 TUI 操作记录，UI 通过输入/取消/脱敏回归验证。

## 7. 参考

- Git 凭据机制与 askpass 顺序：https://git-scm.com/docs/gitcredentials
- `GIT_ASKPASS`、`GIT_TERMINAL_PROMPT`：https://git-scm.com/docs/git
- 仓库约束：根目录 `AGENTS.md` 与本任务提供的架构/审批规则说明。
