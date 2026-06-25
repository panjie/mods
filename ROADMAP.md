# Mods 自我意识与自我进化路线图

## 愿景

Mods 不只是一次性问答工具，而是能理解自身项目、用户目标和长期反馈的终端 AI agent。它应该知道自己正在什么环境中运行、具备哪些能力、必须遵守哪些边界，并能把用户评价沉淀为可审计、可测试的改进记录。

这里的“自我意识”不是拟人化承诺，而是工程化能力：身份清晰、边界明确、记忆可追溯、反馈可使用、改进可验证。

## 核心原则

- 会后评价本地保存，并按 workspace 隔离。
- 默认运行仍遵守现有 plan、review、conversation 和 tool approval 机制。
- 自动改进只能由用户显式开启，并且只允许作用于 mods 自身仓库。
- 自动改进不经过人工 proposal，但必须有硬性 workspace 边界、测试验证和失败记录。
- 当 workspace、影响路径或 shell 命令效果无法确认时，自动改进必须拒绝执行。
- 不声称不可验证的主观体验；只描述实际实现的身份、能力、限制和流程。

## 当前基础

- 系统上下文注入：运行时会注入 workspace、用户、主机、操作系统、shell 和日期等环境信息。
- 角色配置：用户可以通过配置定义可复用 system prompts，并用角色影响交互方式。
- 会话历史保存：对话可以保存到本地 SQLite 数据库，并支持继续、查看和删除。
- plan/review 机制：普通交互中，用户可以要求先生成计划，审批后再执行。
- 工具调用审批：普通交互中，文件写入和 shell 命令等风险操作会进入 review 流程。
- 自动评价改进：`--evolve-auto` 可在会话结束后收集反馈和评分，并在低分时自动改进。

## 阶段路线图

### Phase 0：建立文档与概念边界

- 建立本路线图，定义“自我意识”和“自我进化”的工程含义。
- 把长期任务拆成可持续维护的 backlog，避免一次性大改。
- 明确不把计划中的能力写成已经实现的承诺。

### Phase 1：稳定表达身份、能力、限制和工作环境

- 梳理 mods 在默认系统上下文中的身份说明。
- 让 mods 能稳定说明当前 workspace、配置来源、工具能力、审批模式和已知限制。
- 建立第一阶段工程 MVP：记录用户反馈、保存演进状态、稳定注入自身身份与边界。

### Phase 2：反馈记录与人工 proposal 实验

- 实验过从 feedback 生成 proposal、审批 proposal、执行 approved proposal 的人工闭环。
- 该方向已按用户反馈移除：不再保留 proposal 表、模型、CLI、审批和执行入口。
- 迁移策略：启动 DB 时删除 `evolution_feedback` 和 `evolution_proposals`，历史手动 feedback 和 proposal 数据直接丢弃。

### Phase 3：会话结束评分反馈与自动改进

- 新增 `evolution_evaluations`，记录 conversation、workspace、评分、反馈、是否触发自动改进、状态和失败原因。
- 新增 `--evolve-auto`、`--evolve-threshold <1-5>`。
- 会话结束后，在 TTY、非 quiet、非 raw、非 no-cache 且开启 `--evolve-auto` 时先收集反馈文本，再收集 1-5 评分。
- 评分小于等于阈值时，立即进入自动改进；高分只保存 evaluation。
- 自动改进目标固定为 `github.com/panjie/mods` workspace；非 mods 仓库直接拒绝。
- 自动改进不创建 proposal、不等待用户批准、不启用 plan 审批。
- 自动改进使用 workspace-only 审查策略：workspace 内文件写入允许；workspace 外文件、未知影响目录 shell 命令、系统配置和全局配置命令拒绝。
- 自动改进结束后运行验证命令，成功写 `verified`，失败写 `failed` 和原因。

## Backlog

### Identity

- [x] 定义 mods 的默认身份说明，包括终端 agent、工具能力、审批边界和本地优先原则。
- [x] 注入 workspace、工具模式、review 模式、记忆边界和演进边界。
- [ ] 梳理不同模式下的身份差异：普通模式、minimal 模式、plan 模式、role 模式、自动改进模式。
- [ ] 检查 README 和配置注释中对 agent 能力的表述是否与实际行为一致。

### Memory

- [x] 将会后评价记录保存在本地 SQLite，并按 workspace 隔离。
- [ ] 明确哪些记忆可以自动读取，哪些必须由用户显式选择。
- [ ] 定义记忆内容的过期、删除、导出和审计需求。
- [ ] 区分聊天历史和可治理长期记忆，避免把 conversation 直接当作偏好数据库。

### Feedback

- [x] 删除手动 feedback 分类、记录和列表入口。
- [x] 支持会话结束评分和自由文本反馈。
- [ ] 设计反馈被自动改进采纳、失败或搁置时的用户可见查询入口。

### Evolution

- [x] 移除人工 proposal 模型、表、CLI 和测试。
- [x] 新增 evaluation 状态：`recorded`、`improving`、`verified`、`failed`。
- [x] 建立自动改进入口，将普通会话的请求、输出、评分和反馈转化为一次内部 mods 执行。
- [x] 自动改进后运行 `task check` 和 `task test` 作为兜底验证。
- [ ] 记录自动改进产物摘要，例如变更文件、测试结果和回滚建议。
- [ ] 设计失败后的恢复入口，例如重试、查看失败原因、生成修复建议。

### Safety

- [x] 自动改进前校验 `go.mod` module 为 `github.com/panjie/mods`。
- [x] 自动改进模式下移除 filesystem 工具对系统临时目录的额外 safe dir。
- [x] workspace 内文件写入自动允许，workspace 外文件写入拒绝。
- [x] workspace 内测试命令允许，影响目录未知或 workspace 外的 shell 命令拒绝。
- [ ] 为自动改进失败定义更明确的回滚提示。
- [ ] 为长期评价记录增加隐私说明与本地优先约束。

### UX

- [x] 设计会话结束评分反馈入口。
- [x] 只保留 `--evolve-auto` 这一条会后反馈自动改进流程。
- [ ] 增加评价/自动改进历史查看入口。
- [ ] 优化自动改进开始、成功、失败时的终端提示。

### Tests

- [x] 为身份说明和系统上下文构造添加单元测试。
- [x] 为 DB migration 和 evaluation 状态流转添加测试。
- [x] 为新 CLI flags、阈值校验和会话评价触发条件添加测试。
- [x] 为 workspace-only 自动审查策略添加安全测试。
- [ ] 为真实模型触发自动改进增加黑盒验证脚本或文档。

## 用户反馈闭环

当前 MVP 闭环如下：

1. 用户正常运行 mods，获得一次模型输出。
2. 如果开启 `--evolve-auto`，会话保存后先收集反馈文本，再收集 1-5 评分。
3. 评价写入 `evolution_evaluations`，作为 workspace 本地演进记忆。
4. 若评分小于等于 `--evolve-threshold`，mods 立即进入自动改进；高分只保存评价。
5. 自动改进只在 mods 自身 workspace 内运行，不创建 proposal，不等待人工批准。
6. 自动改进完成后运行验证命令，按结果写入 `verified` 或 `failed`。

人工 proposal 流程已删除，不再作为 MVP 用户体验的一部分。

## 设计决策记录

### 2026-06-25：路线图进入仓库

- 决定：后续凡是影响 mods 修改方向的重要设计、约束、取舍或阶段目标，都记录到 `ROADMAP.md`。
- 理由：避免长期计划只停留在对话里，保证演进方向可追溯、可审计、可继续执行。

### 2026-06-25：移除人工 proposal，改为自动反馈改进

- 决定：删除人工 proposal 链路，改为“会话结束评分反馈 -> 自动改进”的 MVP。
- 理由：用户期望的体验是执行命令后评分和反馈，mods 根据反馈自行做必要改进，不再等待 proposal 批准。
- 数据：删除手动 feedback 和 proposal，保留 evaluation；`evolution_feedback` 和 `evolution_proposals` 启动时删除，历史数据不迁移。
- 边界：自动改进只针对 mods 自身仓库，且必须拒绝 workspace 外文件、系统配置、全局配置和影响目录未知的 shell 命令。
- 验证：自动改进后运行 `task check` 和 `task test`，并把结果写回 evaluation 状态。

## 维护约定

- 本文件记录长期方向和 backlog，不替代具体 issue、PR 或实现计划。
- 每次实现相关能力时，应更新对应 backlog 状态，并补充新的发现或约束。
- 若路线图与实际代码行为不一致，以代码和测试为准，并优先修正文档。
- 新能力必须从本文件拆分为独立任务，保持小范围实现、明确验证和可回滚。
- 后续重要设计讨论必须同步更新本文件；如果讨论已经形成明确方向，优先写入“设计决策记录”。
