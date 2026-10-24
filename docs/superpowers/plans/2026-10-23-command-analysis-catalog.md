# 独立命令分析模块与统一语义库实施计划

日期：2026-10-23  
状态：阶段 0 已完成 Windows 本地验证；阶段 1 已收尾并通过 Linux 本地验证，阶段 2 已实现并通过 Linux/race 验证，Windows 真实 host/快照验收待补；阶段 3–6 待实施。
设计依据：[独立命令分析模块与统一命令语义库](../specs/2026-10-23-command-analysis-catalog-design.md)。

阶段 0 结果：[基线、依赖清单和测试记录](2026-10-23-command-analysis-baseline.md)。

## 1. 交付顺序

| 阶段 | 交付 | 行为要求 | 依赖 |
| --- | --- | --- | --- |
| 0 | 样本基线、依赖清单、差分与安全断言 | 不改变生产决策 | 无 |
| 1 | commandanalysis 契约及旧分析搬迁 | 保持行为；不引入新免审 | 0 |
| 2 | 一次解析、统一上下文和有界查询 | 已有事实不丢失；差异显式审查 | 1 |
| 3 | catalog 基础及全部旧命令入口迁移 | 单一语义来源，兼容条目标注限制 | 2 |
| 4 | 角色化目标与审批适配 | 源读取不再当写；移动覆盖源和目的 | 3 |
| 5 | 高频命令逐族扩展和误报改善 | 每项放行扩展有正反例和对比 | 4 |
| 6 | 删除兼容路径、全平台验收与文档 | 单入口、零模型请求、可量化结果 | 5 |

各阶段以可运行提交收敛。阶段 3 建模目标角色，阶段 4 才切换授权使用；避免角色未完成就启用 complete。可将每个大阶段拆成多个评审单元，不同时保留两套生产决策路径或增加用户可见的新旧模式。

不预设误报下降百分比和工期：阶段 0 才能形成可信基线。优先级按样本频率、影响审批次数、可证明性和实现成本排序。

## 2. 阶段 0：建立基线和迁移清单

工作项：

1. 搜索全部 `approval.CommandAssessment`、`Assess*`、`IsReadOnly*`、`ShellInvocations`、`ExternalShellPathFacts`、bridge lifecycle 调用，以及 app 的环境/Git helper 使用。
2. 为现有行为建立 case schema，暂放 `internal/commandanalysis/testdata/cases` 或测试支撑目录；定义输入模式、方言、环境/路径/Git fixture、信任规则、预期事实和审批。
3. 从现有 readonly、coverage、assessment、conformance、execution_context、remote_targets、approval_entrypoint 测试提取有代表性的用例。保留原测试，不为了复用而删除端到端断言。
4. 记录 cp/mv、PowerShell 重定向、混合输入输出、前序上下文变化等当前限制。预期改善单独列出，不提前改变基线期望。
5. 增加测试侧统计：初始解析次数、bridge 次数、Git 查询次数、分析/复核耗时；复核与初始分析分别计数。
6. 在临时目录、本地 Git fixture 中生成冷/热基准。收集可用的脱敏真实误报样本；没有时明确记录缺少真实样本。
7. 建立 `baseline.md`，记录工作树 revision、平台、工具版本、样本数、分母、已知限制及运行命令。

必须保留的安全样本：

- `uniq in out`、`xxd in out`、Git 嵌套写子命令。
- 未知命令重定向到 temp、已知写与未知兄弟调用。
- `--`、选项缺值、前导短横线文件名、带空格文件名、shell 与 literal `$HOME` 差异。
- 外部读源加已授权写目标；移动源在未授权目录。
- junction/symlink 叶子及父目录、目标不存在、目录项删除。
- 多 pushurl、URL rewrite、本地 remote、linked worktree、前序 Git 配置修改。
- secret_env 遮蔽、cwd/PATH 变化、工作区同名程序、provider 写。

出口：能够分别回放事实和审批；安全错误/已知保守行为有独立标签；纯迁移差分规则明确。用户允许记录不得自动标为 read。

## 3. 阶段 1：建立包边界，保持当前行为

实施与验证记录：[阶段 1 收尾](2026-10-24-command-analysis-stage1.md)。

### 1A. 契约与兼容

新增 `internal/commandanalysis/input.go`、`result.go`、`engine.go`、`dependencies.go`。定义 tagged Input、分析事实、零值、错误分类和显式信任。第一步保留 legacy 字段桥接，不改变目标角色的生产含义。

在 `internal/app/command_analysis.go` 建薄适配器，负责工具 JSON 解码、实际 cwd、shell 身份、process binding 和配置快照转换。移出分析算法，app 不直接解释命令选项。

approval 中临时包装依赖新包，绝不能形成反向导入。`CommandAssessment.AccessIntent()` 逐步移至 app 转换函数，旧包装仅做兼容转换。列出包装删除清单。

### 1B. 按职责搬迁

| 来源 | 目的 | 留在原处 |
| --- | --- | --- |
| approval 的 assessment/shell_static/readonly/coverage/writable_dirs | commandanalysis 对应文件 | access/matching/rules 及非命令授权类型 |
| shell_parse、POSIX 动态/scalar 分析、invocations | commandanalysis parser/组合层 | 无命令解析器留在 approval |
| ps_bridge/ps_literal/ps_probe/readonly_ps | commandanalysis 的 PowerShell 层 | tools 的实际执行器 |
| git_static | commandanalysis Git 语义/查询 | 无授权逻辑随之迁移 |
| app shell_assess/stable_env/posix_env/literal_target | commandanalysis 编排/解析 | app 工具参数适配、配置获取 |
| app remote_targets | commandanalysis Git/候选分析 | URL 脱敏、显示文案 |
| command_constraint/reviewability/script_file/command_families | commandanalysis 诊断层 | review UI、分页和风险展示 |

文件含混合职责时按函数迁移，不机械整文件复制。相应纯测试移动到新包；app 留集成测试。Windows build tags、嵌入 bridge 脚本路径、CloseBridge 清理调用一并调整。

出口：生产从新包调用；无 app/config/provider 反向依赖；行为差分无未解释变化；基线构建/测试通过。此阶段不宣称误报改善。

## 4. 阶段 2：统一解析、上下文及查询

实施与差分证据：[阶段 2 记录](2026-10-24-command-analysis-stage2.md)。

工作项：

1. 引入私有 ParsedCommand，存储 AST/IR、node ID、source span、调用关系、重定向和上下文事件。
2. 将只读、写目标、结构诊断、调用枚举改成消费 ParsedCommand；Git 不再调用 `ShellInvocations(command)` 重新解析。
3. 升级 PowerShell IR 版本，增加重定向目标、参数字面性和必要节点关系；维护协议 fixture 与版本失败测试。信息不足仍为 gap。
4. 将环境读取改成按需快照；secret_env 只提供遮蔽名称。字面赋值/自动变量/环境物化使用同一个上下文。
5. 抽出有限 PathResolver、GitResolver 和 parser host 接口，收拢现有只读查询及有界输出实现。查询接口不得接受任意 shell 文本。
6. 统一 ctx、预算、取消和 bridge 生命周期；保留既有输入限制，超限降级为可解释 incomplete。
7. 复核先维持全量重新分析；同次调用内查询可去重，跨审批不得沿用可变状态结果。

验证：parse spy 证明每次 Analyze 只有一次主解析；多次只读遍历允许。注入查询失败、取消、输出超限、bridge 协议错误、环境变化；验证不会生成 complete。对同一 AST 节点不丢失嵌套未知调用。

出口：Git/路径/诊断共享解析结果；测试不需要构造 Mods 即可回放完整分析；Windows 真实 bridge 与离线 IR 的事实一致。新增可识别重定向的行为变化放到后续显式语义提交启用。

## 5. 阶段 3：建立并统一命令语义库

### 3A. 最小可用 catalog

新增 catalog、通用选项绑定、条目校验及 InvocationFacts。先以 uniq/xxd 为端到端样板，覆盖已有输出操作数与 reverse 语义，不扩大参数范围。

统一操作顺序：确认命令身份 → 匹配条目 → 解析选项/操作数 → 判断前提 → 一次生成 effect/targets/coverage/gaps。readonly/writable/coverage 消费同一结果，不再各自分派。

条目校验至少检查：ID 唯一、别名与方言/身份无歧义、选项定义一致、子命令文法无冲突、每条规则有用例、未知分支不能默认 complete。注册过程零 I/O。

### 3B. 按族迁移所有旧入口

1. 文本命令、文件修改、PowerShell cmdlet、Git、wrapper 按设计批次迁移。
2. 把旧 `readOnlyCommands`、`subcommandReadOnly` 和 PowerShell 表转为 catalog 条目；未审计项标注 compatibility 状态及已知前提。
3. 删除已迁移命令在 `invocationTokensReadOnly`、`writableTargetsFromTokens`、`boundedWriteInvocation` 的重复规则。最终旧函数若仍存在，只能委托 catalog。
4. 保留 native 与 shell builtin/cmdlet 区别，验证 Windows process_run 不套 PowerShell alias，避免通过 basename 绕过绑定检查。
5. 用户显式只读信任作为单独 evidence，不隐式加入静态命令库；不得吞掉外部重定向或兄弟节点。

本阶段兼容迁移与安全修复分开评审。发现旧表的错误 read 时立即写失败用例并独立修复，不把已有输出当成正确答案。不得通过扩大未知选项支持来“消除差分”。

出口：所有已有命令从统一 registry 分派；每个 invocation 只做一次语义求值；没有第二套完整写名单；确定性 fixture 和现有 app 回归通过。

## 6. 阶段 4：切换角色化目标与审批适配

主要修改：`commandanalysis/result.go`、目标解析/组合层，`internal/app/review.go`、`request_session.go`、review summary/presentation 及新转换测试。

工作项：

1. 为所有 migrated 条目产生 read/write/candidate 目标；legacy KnownDirs 只作过渡展示，不能继续推断完整写范围。
2. 保留叶子操作数直到操作语义解析完成。加入 follow-leaf/remove-entry/rename-entry、源与目标角色、目标不存在和目录项父目录处理。
3. 建立单一 `commandResultToIntent`，显式映射 split 字段、unknown、provider、unresolved、coverage 与 gap。单测覆盖全部枚举和非法/零值组合。
4. literal fallback 只产生 candidate；禁止 candidate 进入 WriteDirs 或规则候选。读取动态表达式不自动制造未知写。
5. 将审批后比较从含噪声的结果比较改为授权语义比较；验证依赖/目标变化触发一次重新审批、持续变化零执行。
6. 逐步改 UI 展示读源/写目标/未知部分，保持分页和原始命令；非命令工具展示不受影响。
7. 删除事实对象旧 `AccessIntent()` 生产调用，统一转换用于初始审批和复核。

关键验收：

- 输入目录未获写授权、输出目录已获写授权时，完整复制分析不要求对输入授写权限。
- 移动不能仅凭目的目录规则放行；源目录项也必须覆盖。
- 无证据 read、未知程序已知路径、未知程序重定向 temp 仍询问。
- 原生 filesystem grant、会话规则、ReviewAlways/ReviewNever 和非交互行为保持既有语义。
- 目标链接变化、程序绑定变化、Git 配置变化不会沿用过期授权。

出口：混合目标闭环通过；没有从 KnownDirs/cwd 推断写权限的命令兼容路径。只有此后新增规则才能宣告目标完整。

## 7. 阶段 5：逐族降低不必要审批

每个命令族作为独立评审单元；不一次扩大全部命令和参数。

| 单元 | 实施内容 | 正例验收 | 反例验收 |
| --- | --- | --- | --- |
| 文本 | 高频缺失参数、输出角色、受支持原地写 | 真实只读参数免审；明确输出复用目标规则 | 未知参数、解释器表达式、额外输出不误放 |
| 普通文件写 | 规范选项、多路径、目录项/链接 | 完整目标能命中已有规则 | 额外写源、链接逃逸、未建模递归保持询问 |
| cp/mv | 普通非递归文件、明确源/目的类型 | 复制只授权写目标；移动要求两端 | backup、递归、链接、跨卷等未建模形式不 complete |
| PowerShell | 高频 cmdlet 参数、LiteralPath、可靠重定向 | 相同事实在真实 bridge/fixture 一致 | provider、数组绑定不明、splatting 保留 gap |
| Git | 受支持全局选项、正确前序上下文、远端及仓库完整展示 | 已知读取前序不无条件污染后续解析 | hook/helper/配置变更不因 origin 已知而放行 |
| wrappers | 现有 env/xargs/find 受限形式共享内层规则 | 可证明只读的组合保持快速路径 | 动态程序名、嵌套未知执行不继承只读 |

每单元交付：

1. 语义条目及 revision、支持/不支持形式说明，必要时附对应实现的官方语义依据。
2. 正例、写入反例、缺值/未知选项、平台差异和组合例。
3. 事实→授权→dispatch 验证；负例默认用 spy，文件正例只在隔离目录执行。
4. 固定样本集前后报告：Ask→Allow、Allow→Ask、目标变化、gap 变化、耗时。
5. 全部新自动放行都有人工可解释证据；没有足够依据的调用仍单次询问，不以达成百分比目标为理由放宽。

出口：安全集零错误放行；至少首批目标场景出现经验证的审批改善；剩余高频 gap 有明确后续列表。不能把构造样本改善宣称为真实用户误报率下降。

## 8. 阶段 6：清理、验收与发布说明

1. 删除 approval 中命令分析薄包装和废弃字段，app 仅保留工具输入/结果转换、生命周期和展示。
2. 加包依赖和目录检查，防止 commandanalysis 导入 app/approval/tools/config/provider；校验不存在 catalog 外的只读/完整写授权入口。
3. 移除生产旧分析器；差分 harness 改用阶段 0 固化结果和人工语义 oracle，不永久维护旧实现。
4. 补组合性质和 fuzz：未知节点不能被 complete 节点覆盖、未知选项不升级信任、输入不崩溃、重复合并稳定、literal 参数不被插值。
5. 复测冷/热基准，解释明显延迟/查询数变化；不以跨请求缓存掩盖重复解析。
6. 更新 AGENTS.md 的 shell classification、app map 和测试入口；更新 2026-10-22 架构章节，给 2026-08-07 历史 LLM 章节增加已被取代说明。
7. 发布说明列出已支持形式、仍需审批的情况、任何有意收紧及实际测试平台。

最终出口采用设计第 10 节，任何缺失平台验证明确标为待完成。

## 9. 验证命令与平台矩阵

以下为后续实施阶段命令；阶段 0 的实际运行记录见基线文档。新包创建前不运行不存在的包路径。

```text
go test ./internal/commandanalysis -count=1
go test ./internal/approval ./internal/app ./internal/tools -count=1
go run github.com/go-task/task/v3/cmd/task@v3.51.1 check
go run github.com/go-task/task/v3/cmd/task@v3.51.1 test
```

最终在 CI 使用既有 `ci` task，Ubuntu/macOS/Windows 根模块均验收；race 在受支持 runner 运行，并记录因环境缺失无法执行的项。具体新测试名称在实施时确定，不在计划中假称已经存在。

| 层次 | 验证内容 | 环境 |
| --- | --- | --- |
| 纯 catalog/组合 | 选项、角色、complete/gap、fake 查询 | 全平台 |
| POSIX parser | AST、重定向、引用/展开、嵌套 | 全平台纯 Go |
| PowerShell IR | 协议回放、节点、参数与重定向 | 全平台 fixture |
| PowerShell bridge | 实际 host、协议和取消/清理 | Windows；明确记录 5.1/pwsh 各自覆盖 |
| 文件/Git | symlink/junction、leaf、linked worktree、多 URL | 隔离本地 fixture，平台各自验证 |
| 审批/dispatch | auto/always/never、拒绝、缺交互、重审、执行次数 | 现有 app 集成测试 |
| 性能 | 冷/热解析、查询、复核、分配 | 固定机器/fixture；初始与复核分开 |

不运行 provider integration、不请求 API key、不发网络 push、不触发 UAC。所有真实副作用仅在测试 fixture 内；链接清理不跟随链接删除外部目标。

## 10. 回退与评审规则

结构迁移可按提交回退；语义扩展按命令族/revision 回退，不需要恢复旧模式或模型分类。无数据库迁移，已保存会话规则继续存在；回退导致覆盖不足时重新询问，不扩大旧规则范围。

每个评审单元必须列出：模块职责变化、行为差异、支持边界、证据/完整性依据、安全反例、测试结果和剩余缺口。未知选项、动态上下文及未建模执行不得因体验目标获得隐式信任。

阶段 0 的基线、阶段 4 的角色转换、阶段 5 的每次授权扩展是独立检查点；不能把它们合并成无法定位行为变化的大补丁。
