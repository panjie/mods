# 命令审批重构分阶段实施计划

日期：2026-10-22  
状态：四项 P1 与阶段 0–4 的核心主链路已落地；2026-10-23 Windows 本地验收通过。全平台发布验收尚待 CI / race。  
设计依据：[命令审批核心重构设计](../specs/2026-10-22-command-approval-refactor-design.md)。

## 1. 实施顺序与交付边界

| 阶段 | 交付 | 优先解决 | 依赖 |
| --- | --- | --- | --- |
| 0 | 四项 P1 永久回归用例与行为差异表 | 固化证据，避免误修 | 无 |
| 1 | 最小事实契约、保守授权、只读参数修复、LLM 建议隔离 | P1-1、P1-2；保守阻断不完整远端规则复用 | 0 |
| 2 | 真实路径准备与按调用写凭据 | P1-4 | 1 的 intent 契约 |
| 3 | Git invocation 全量解析与远端完整性 | P1-3；恢复有证据支持的 Git 快速路径 | 1；使用 2 的本地路径语义 |
| 4 | 统一 prepared call、清理重复分析、UI/诊断与全平台验收 | 收敛架构与兼容性 | 1、2、3 |

每阶段独立可评审；按顺序实施，不为等候后续抽象而推迟已完成的 P1 修复。阶段 1 的保守降级不是远端解析功能已完成；P1-3 的所有目标展示与覆盖验收仍必须在阶段 3 完成。

不在本轮引入：沙箱、通用脚本工具、强制拆分/纠正预算、新权限类型、数据库迁移、全量配置重做、独立审计服务。权限撤销 UI 与更细资源级授权作为后续事项。

## 2. 阶段 0：固化回归和基线

将评审中的临时观察转成有明确安全断言的永久测试，不以日志输出充当断言。测试提交与修复提交一起合并，避免主分支持续失败。

建议新增/扩展：

- `internal/approval/readonly_test.go`、`readonly_ps_test.go`、`shell_static_test.go`：输出操作数和嵌套可写子命令。
- `internal/app/shell_assess_conformance_test.go`、Windows 对应文件：复合命令和未知程序重定向的事实及 coverage。
- `internal/app/remote_targets_test.go`：多个 pushurl、两个 push、默认远端、`--repo`。
- `internal/app/approval_entrypoint_test.go`、`command_dispatch_test.go`：完整调用链的执行次数、候选规则和拒绝行为。
- 新增 `internal/app/approval_path_binding_test.go`：真实原生文件工具 + junction/symlink，验证未授权目录没有写入。

隔离规则：临时目录仅用于 fixture；授权范围与安全临时目录分开配置，不能让系统 temp 豁免掩盖规则错误。Windows cwd 内 allowed/other 场景应完整覆盖；链接 fixture 的清理不得跟随链接删除目标。Git 仓库本地初始化，不 fetch/push 网络远端；待审未知命令用 dispatch spy，不执行危险 payload。

记录旧行为及新预期，尤其：

- `TestComplexCommandDispatchUsesOrdinaryApproval` 中 classified-read 自动运行的用例将改变。
- `TestUnknownEffectWithKnownOriginHasAlwaysRule` 的目标提示不足以继续生成规则。
- `TestUncertainEffectTargetPolicy`、`access_test.go` 中未知副作用写临时目录的用例，必须区分“已证明完整范围”与“仅提取出路径”。
- 原始参数不改写、人工批准执行一次、拒绝零执行的既有断言保留。

阶段出口：四项 P1 在未修复实现上可稳定触发失败，反向的正常只读/正常临时写用例同时存在；记录本地基线。

## 3. 阶段 1：授权依据与完整性最小闭环

建议拆成两个紧邻的评审单元，覆盖写生产者的迁移和策略启用必须原子落地，不能中途默认所有非空目标为完整。

### 1A. 数据契约、聚合与统一策略

修改主要文件：

- `internal/approval/assessment.go`、`access.go`、`matching.go`。
- `internal/approval/shell_static.go`、`readonly.go`、`readonly_ps.go`。
- `internal/app/shell_assess.go`、`review.go`。
- 各原生 intent extractor、`internal/mcpclient/mcp.go` 和对应测试。

工作项：

1. 引入 evidence、write coverage、gap code，零值为未知/不完整；保留必要的兼容派生字段。
2. 建立合并函数及测试，POSIX AST 和 PowerShell IR 的未知可执行节点全部贡献 gap。
3. 删除 LLM 分类器、结果解析、缓存、advice 模型和配置提示词入口；未知行为直接进入普通审批。
4. `ClassifyAccess`、`RulesAllowIntent`、候选规则生成共用完整性资格判断。临时豁免与保存规则都不能绕过 incomplete。
5. 审计全部 AccessIntent 生产者，按实现或明确的信任契约逐个赋值；无法说明来源的一律 incomplete。
6. 暂未完成语义解析的 Git push、任意脚本/解释器、配置可触发未知 hook 的调用保留缺口。不能为维持旧放行率伪造 complete。
7. `ReadOnlyCommandPolicy` 继续作为用户显式信任，单独标记来源；其存在不能清除同一调用中其他未知节点。
8. 更新当前政策文档和 `AGENTS.md` 中受影响段落；明确这次为有意收紧，不把预期变化当作回归放宽。

### 1B. 首批命令语义修复

1. 为 `uniq`、`xxd` 的支持参数识别输出目标；未知参数形式不标为只读。
2. 为 `git reflog` 的查询和修改形式建立显式区分。无法安全解析修改范围时保持单次审批。
3. 让 POSIX、PowerShell 外部命令和 literal argv 共用这些参数规则，消除各入口之间的绕过。
4. 从泛化只读表移除存在输出或嵌套修改语义而未做参数校验的上述入口；增加 `--`、空格和选项边界用例。

阶段出口：

- P1-1 三类例子不再以可信 read 免审；P1-2 在 auto/always 中均不能靠 temp 或 saved rule 放行。
- 直接验证未知命令保留不完整目标，不能依靠临时目录或已有规则放行。
- 参数已知的只读命令与完整临时写保持预期；非交互 incomplete 调用零执行。
- 支持模式、minimal、拒绝、批准一次、旧规则不授权等组合回归通过。

## 4. 阶段 2：真实路径与本次写权限

主要文件：`internal/pathutil/resolve.go`、`internal/tools/filesystem_intent.go`、`path_safety.go`、`filesystem_write.go`、download/patch 实现、`internal/tools/tools.go`、`internal/app/request_session.go`、`review.go`、`internal/approval/matching.go`。

工作项：

1. 为原生文件操作增加能返回错误的 `PrepareAccess`，产生 typed local target；不要在现有纯 extractor 中吞掉真实路径解析错误。
2. 对读写、删除链接、复制、移动、目录创建、patch、download 逐一记录真实的叶子跟随语义；复用 `ResolveThroughExistingParent`，补充明确的 no-follow-leaf 入口。
3. 审批前对实际写对象求真实路径，再取得批准目录。safe-dir 判定同样基于实际对象；已知 shell 目标使用对应语义，无法确定则 incomplete。
4. 将 requestApproval 结果转换为按调用的写凭据；审批成功后才注入。读取 cwd、外部读路径与写权限分离。
5. 写工具执行时只接受本次真实写范围；移除写路径中“在 cwd 即可”的默认授权。覆盖全部 mutating built-ins，避免只修 fs_write_file。
6. 执行前复核目标变化，未产生副作用时返回 stale-target；调度层重新准备/审批，禁止工具自行扩权。
7. 新规则保存真实路径；旧规则按稳定词法值匹配，不重新跟随别名、不凭磁盘类型改成父目录；保存/恢复同一会话继续测试。
8. 调整直接调用原生工具的测试，使其显式携带写凭据，测试 helper 不能进入生产绕过路径。

阶段出口：

- Windows `allowed/link -> other` 与 POSIX 对应 fixture：仅 allowed 规则时 other 没有任何写入。
- 实际批准 other 后，允许按原有操作语义完成写入；UI 展示真实目标。
- 安全临时目录中的链接不能将豁免带到非临时实际目标。
- 链接在审批等待期间变更时，不按旧批准写新目标。
- 删除链接不意外删除链接指向内容；复制/移动/patch/download 语义与权限一致。
- `--continue` 不把旧别名规则迁移成新目录权限，规则持久化无需 schema 修改。

## 5. 阶段 3：Git 目标解析完整化

主要文件：`internal/app/remote_targets.go`、`shell_assess.go`、`internal/approval/git_static.go`、`assessment.go`、`shell_static.go`，及相应测试。可新增 `git_targets.go` 等小模块，避免继续扩大原正则 helper。

工作项：

1. 解析器提供所有 Git invocation，而不是 `FindStringSubmatch` 的第一条；literal argv 使用相同入口。
2. 建立受支持参数表：明确 remote/URL、`--repo` 两种形式、受支持 `-C` 与引用形式；未支持全局配置覆盖保留 gap。
3. 只读查询所有 push URL；保留有效目标的同时记录任何失败目标，不能静默丢弃。
4. 实施默认远端选择的受支持子集，依据本地配置和 Git 实际规则建立 fixture；无法证明时单次审批，不默认 origin。
5. 测试 URL 改写、多个远端、引号/空格、分支配置、无 remote、无法查询、命令内先改配置再 push、每次环境覆盖。
6. 把 local path remote、网络 origin、未知 helper 分开；origin 规范化继续复用现有实现。
7. 查询 helper 使用固定可执行文件、取消、超时和输出上限；不执行待审命令，不调用网络端点，不解析秘密值到日志。
8. 记录必要上下文快照并在执行前复核；远端变化进入新审批。
9. 对 hook/helper 等未限定行为保持整体 incomplete。只有作用模型和所有目标均有依据时才恢复会话规则快速路径。

阶段出口：

- 多 pushurl 和多个 push 的完整已知目标全部展示，不能只显示或授权第一个。
- 仅第一个 origin 有规则时，实际 dispatch 为零；存在第二个未知目标时不生成会话规则。
- `git push`、`--repo` 选择正确或明确 unresolved，不返回错误的 origin 作为完整结果。
- 移除无必要的 hook/未知因素后，受支持且完整的调用仅在全部范围被批准时才可运行；更复杂形式仍可 Allow once 原样执行。

## 6. 阶段 4：架构收敛与发布验收

工作项：

1. 将现有参数归一化、process binding、prepared access 和目标快照收敛到内部 `PreparedCall`，供审批展示、规则匹配和执行共同使用；不改工具公开参数 schema。
2. 明确 secret_env 引用与有效环境前提：审批阶段不展开秘密到 UI；执行前若有效环境改变授权假设，不能沿用原凭据。
3. 将文本/Git/文件操作的参数语义集中为小型分析器，保留 shell 方言解析；删除已被替代的正则授权路径和重复规则，不一次性重写全目录。
4. 删除独立分类模型请求及缓存，不为未知命令额外调用模型。
5. UI 显示确定目标、未限定副作用、实际会话授权范围；分页检查范围也覆盖新增说明。
6. 扩展现有 debug trace，区分静态/显式信任、coverage、匹配规则、真实目标及复核结果，不新增持久化数据库。
7. 清理派生兼容字段和政策重复，更新文档、测试名及行为变化说明；对旧设计的失效章节加明确替代指向。

阶段出口：全部 P1 回归、现有读取/临时写/审批交互/持久化/UAC 默认测试通过；每条自动允许结果能解释依据；无调试日志秘密泄漏；全平台 CI 完成。

## 7. 验证矩阵与命令

| 层级 | 关键断言 |
| --- | --- |
| 分析器 | 语法和参数改变能改变语义；未知选项保留缺口；目标角色正确 |
| 合并 | 增加未知动作不能把 incomplete 变为 complete；重定向不能限定被调用程序 |
| 策略 | 不完整写不能被 temp、任意目录规则或单个 origin 消除；全部目标均覆盖才可复用 |
| dispatch | 无批准零执行；批准一次仅执行一次；参数与实际审核内容一致 |
| 路径 | 字面/真实路径、叶子链接、junction、未创建路径、等待期间变化及会话恢复 |
| Git | 多 invocation、多 pushurl、默认远端、参数边界、局部配置改变、helper 不确定性 |
| 交互 | 所有页面可见、unknown 原因清楚、A 选项资格正确、deny/cancel 保持区分 |

每阶段先运行变更相关 package 的定向测试。合并前至少运行：

```text
go test ./internal/approval ./internal/app ./internal/tools ./internal/pathutil ./internal/mcpclient ./internal/tooling ./internal/session
go run github.com/go-task/task/v3/cmd/task@v3.51.1 check
go run github.com/go-task/task/v3/cmd/task@v3.51.1 test
```

最终按仓库 CI 在 Ubuntu、macOS、Windows 运行；需完全镜像 CI 时运行 task `ci`。在可用的 race 环境对审批 channel/取消/缓存相关变更运行定向 `-race`；不把未运行的平台或 race 宣称通过。默认测试不能触发 UAC，不使用真实 provider key 或远端 push。

引入少量性质测试/有界 fuzz：组合未知节点的保守性、目标集合扩大不能降低所需权限、规范化不能扩大已保存规则、别名不扩大范围。测试比较的是安全性质和实际副作用，不复制实现函数作为 oracle。

## 8. 发布、回退与剩余边界

不增加默认放宽开关，不以回滚安全检查来解决审批变多的问题。阶段 1 后暂不能可靠建模的命令保持单次审批；后续用具体命令分析器恢复快速路径。

如果发现某个分析器错误，其回退为该形式 incomplete，保留其他已修复入口。阶段 2 的路径重构应以入口断言保护：无有效本次写凭据不得落盘；不能回退成 cwd 隐式写授权。

预期体验变化是未知脚本、LLM-only read、复杂 Git 配置需要更多单次批准。通过正常只读/完整临时写的反向用例、审批触发原因和耗时观测控制误报，不以降低安全断言作为性能优化。

剩余边界：原生路径复核与执行之间仍可能有系统级竞态，任意 shell 子进程没有 OS 级约束；MCP 继续依赖现有声明信任；origin 规则仍覆盖整个 origin；持久审计与规则撤销 UI 留待后续。以上不影响四项已复现场景必须修复的验收要求。

## 9. 本轮实施记录（2026-10-22）

- 阶段 0：四项 P1 永久回归已建立，并确认修复前失败、修复后通过。
- 阶段 1：evidence / coverage / gaps、参数语义修复和 advice 隔离已完成。未知调用不再借用已知目标；随后已删除全部可选模型建议基础设施，审批路径不发起模型请求。
- 阶段 2：全部原生可写文件工具及下载接入准备和按调用凭据；覆盖真实叶子、缺失父目录、参数绑定、内容秘密引用、链接变化与重新审批。存量规则按词法路径匹配，不把文件提升为父目录权限。
- 阶段 3：移除首条 push 正则，枚举所有 invocation，支持多个 pushurl、默认远端优先级、`-C` / `--repo`、URL 改写和本地远端。执行前重新评估，目标变化重新审批。Git 查询有取消、超时和输出上限，不执行待审命令、不联网 push。
- 阶段 4：`preparedToolCall` 统一审核与执行参数；展示完整写范围和 incomplete 原因；诊断输出区分事实来源和完整性。后续删除旧 LLM 配置字段、提示词和缓存，保留确定性分析及其回归。

本轮有意保守的边界：Git 写操作的 hooks/helper 副作用尚无完整模型，因此不恢复 Git 写的会话规则快速路径。复杂脚本、方法调用、不能绑定真实叶子的动态表达式继续允许单次人工批准；不能只因为路径已提取或环境变量已展开就标为 complete。模式/二进制 patch 若缺少可解析文件头，标记 incomplete 并展示仓库范围。跨平台 CI 与 race 结果须以实际执行记录为准，本地通过不代表这些检查已执行。

## 10. 本地验收记录（2026-10-23）

- `go run github.com/go-task/task/v3/cmd/task@v3.51.1 check`：通过（`go build ./...`）。
- `go run github.com/go-task/task/v3/cmd/task@v3.51.1 test`：通过（根模块 `go test ./...`，含四项 P1、审批入口、真实写入、junction 变更重审、Git 目标变更重审和现有回归）。
- 修改的 Go 文件通过 `gofmt` 格式检查；`git diff --check` 通过；修改文件保持 LF。
- Git 直接 URL 重写使用有界只读 `git config --null --get-regexp` 查询；最长前缀、pushInsteadOf 优先且不递归、insteadOf 回退均与真实 Git remote 查询对照。查询失败保留 unresolved，不能生成可复用权限。
- 未运行 Linux/macOS CI、race、真实 provider 集成和需要桌面同意的 UAC 集成；未执行真实远端 push。本地通过不替代这些发布检查。

本轮代码和回归已完成，未创建提交或 PR。Git hooks/helper 完整建模、沙箱、持久审计和规则撤销 UI 不属于本轮已交付能力。

## 11. 删除 LLM 评审基础设施（2026-10-23）

按用户要求删除 `shell_classify.go` 中的模型请求、响应解析和 LRU 缓存，删除 `AssessmentAdvice`、测试注入钩子、建议展示，以及配置结构、帮助、默认模板和内置提示词目录中的分类器入口。确定性回归迁移到 `shell_assess_test.go` / `shell_assess_windows_test.go`，保留路径、Git、未知行为与审批分派断言；删除仅针对 LLM 的解析和缓存测试。

旧 YAML 中的 `shell-classify-prompt` / `prompts.shell-classifier` 被忽略，默认模板和配置结构序列化不再输出这些字段；局部 YAML 编辑可保留旧文本，但其不再生效，其余配置正常读取。专项回归覆盖此迁移行为。没有引入沙箱。

本次删除完成后重新运行 `task check`（`go build ./...`）和 `task test`（`go test ./...`），Windows 本地全量通过；`gofmt`、LF 与 `git diff --check` 检查通过。未运行其他平台 CI 或 race。
