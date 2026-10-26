# 阶段 6：清理与本机验收

2026-10-26 更新：Windows 全量测试、PS7/PS5.1 真实桥、junction 和平台快照已补齐，详见 [Windows 验收记录](2026-10-26-command-analysis-windows.md)。以下为 2026-10-25 的历史结果，未完成项以新记录为准。

日期：2026-10-25。第六阶段代码、结构检查、性质/fuzz、基准与发布说明已实施；真实 Windows/macOS 验收缺口未关闭，不能宣称全平台最终出口完成。本轮与此前第五阶段后续改动已按用户要求合并到本次本地提交，父提交为 `54060c5`，未推送。

## 清理结果

- 删除 `internal/approval/command_compat.go` 的所有命令分析转发函数、命令类型包装和 `CommandAssessment.AccessIntent()`。app、MCP 来源规范化及相关测试直接使用 commandanalysis。approval 只保留 `evidence.go` 中工具中立的 coverage/gap 类型词汇，与规则匹配所需的 origin 规范化调用。
- app 生产分析统一经 Engine，调用方保持工具输入、程序绑定、结果快照、角色化授权转换和展示。没有引入另一份 parser、命令表或 LLM 分类。
- 移除旧适配器专属 `approval/command_intent_test.go`；其安全边界由 app 的真实转换测试、`TestReadAuthorizationRequiresCompleteStaticOrTrustedEvidence` 与已有 `command_targets_test.go` 覆盖。全部原有 app `AccessIntent()` 调用切换为 `commandResultToIntent`。
- 旧测试中的 KnownDirs/Dirs 混淆改为显式检查：展示仍保留目录事实；未知调用的候选路径不形成写组；混合读写使用 ReadDirs/WriteDirs。合成 read fixture 补齐完整证据，不放宽生产转换以迁就旧测试。
- 仍在运行时使用的 KnownDirs、DynamicProbe、StaticRead 等展示/诊断/解析字段未机械删除；它们不授予写权限。catalog 的 Compatibility 标记保留，表示未逐一重审的旧语义，不表示保留第二个分析器。

## 结构和性质保护

`TestCommandArchitectureHasOneProductionBoundary` 检查 app/approval 生产文件：禁止引入 shell parser、恢复兼容文件/旧转换、从 app 直接进入旧静态评估 API，或在 approval 声明命令分析入口。approval 对 commandanalysis 只允许共享 coverage/gap 与 origin 规范化。

`TestAnalysisPackageDoesNotImportPolicyOrApplication` 递归检查分析目录，包括未来子包；内部依赖只允许 pathutil 和 commandtrace，不允许 app/approval/tools/config/provider。

新增组合测试与 fuzz：

| 目标 | 性质 | 本机结果 |
| --- | --- | --- |
| TestUnknownSiblingNeverInheritsCompleteCoverage | 顺序、管道、条件组合中的未知调用不能被已知写覆盖 | 通过 |
| FuzzCommandAnalysisNoPanic | 有界输入不崩溃；不可变依赖下结果确定；clone 稳定；未知后继保留 incomplete | 101818 次，通过 |
| FuzzAuditedOptionsNeverHideUnknownFlag | 已审计文件/文本命令的未知首选项不升级为 read/complete | 64303 次，通过 |
| FuzzLiteralArgvAndMergeStability | literal argv 不插值；origin 去重合并重复执行稳定 | 95445 次，通过 |

每组 `-fuzztime=10s -parallel=2`；依赖注入避免执行待审命令或访问 Git 网络。总计 261566 次，未产生失败语料。短期 fuzz 不是正确性的穷尽证明。

## 差分与性能

原 23 条 Linux 事实、第五阶段新增 10 条事实以及 stage2 work 快照均直接比较通过，未执行 update。阶段 6 不再扩大授权语义；审批改善沿用第五阶段的独立基线，不维护旧实现作为生产路径。

暖启动 app 基准每样本 200 次，完整记录 `internal/app/testdata/command_analysis/stage6_benchmark_linux.txt`。例如扩展集 touch-reference 93745 ns/op、env-touch 69486 ns/op、git-global-read 58683 ns/op。与第五阶段单轮测量有波动，分配次数/查询计数未显示新增重复解析；没有引入跨请求结果缓存，也不将单轮计时差异宣称为性能提升。

Linux PowerShell 7.6.8 真实桥生命周期各测 5 次：cold 297407358 ns/op，warm 3944144 ns/op。cold 含进程启动/结束，warm 复用连接但每轮真正解析新请求。差异主要来自宿主启动，测量不代表 Windows 5.1/7 冷启动结果。Windows 专用 cold-host 基准仍待实机运行。

## 平台矩阵

| 检查 | 结果 |
| --- | --- |
| Linux `go build ./...` / `go test ./...` | 通过 |
| commandanalysis/app/commandtrace 带计数测试 | 通过；既有 work 快照不变 |
| commandanalysis/app/approval race | 通过 |
| 三组 fuzz | 通过，见上表 |
| Linux PowerShell 7.6.8 真实脚本 | 15 条解析与 7 条隔离执行正例通过；不替代 Windows |
| Windows amd64 根模块、app/commandanalysis 测试 | 交叉编译通过；没有运行 |
| macOS arm64 根模块、app/commandanalysis 测试 | 交叉编译通过；没有运行 |
| Windows 真实桥/5.1/junction/UAC、平台快照 | 待完成；默认测试不触发 UAC |
| macOS 实际测试及平台快照 | 待完成 |

结构测试与普通测试覆盖全部平台文件的源代码检查，但不能代替实机路径语义和命令执行。最终全平台出口仍以真实运行结果为准。

## 文档与发布

已更新 AGENTS.md、2026-10-22 架构职责表、2026-08-07 历史 LLM 设计的取代说明及 Handsoff.md。用户可读变化见 [发布说明](../../command-analysis-release-notes.md)。代码已按用户要求本地提交，未推送；平台缺口如实保留。
