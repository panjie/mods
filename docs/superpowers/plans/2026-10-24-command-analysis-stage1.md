# 阶段 1：模块迁移收尾记录

日期：2026-10-24。基于 WIP `34b3423`，收尾修改已随阶段 2 一起纳入本地提交，未推送。阶段 1 代码与 Linux 收尾完成；最新 Windows 真实 host/快照复测待补，不宣称跨平台验收完成。

## 实现与审查

- `commandanalysis.Engine` 接收显式方言、cwd、shell/argv、遮蔽环境名称和信任配置副本。依赖边界测试禁止反向导入 app/approval/config/tools/provider。
- app 保留工具输入、配置和程序绑定适配；初始审批和复核共用 `commandResultToIntent`，当前仍委托 legacy `AccessIntent` 映射。review/dispatch 和授权规则没有搬进分析器。
- `Result.Clone` 复制所有 slice/map 和嵌套 Reviewability.Reasons。本次增加覆盖全部当前引用字段的修改隔离测试，原有契约/输入快照测试继续保留。
- 输入错误和已取消 context 由 Engine 返回 unknown/error，app 保持旧接口，转为 unknown/incomplete。新增 app 测试确认这些结果、零值和无证据 read 都不能自动读取免审，也不能借助临时目录或目录规则获得写授权。
- 当前不改变 app 错误接口或 ReviewNever 语义。分析中途取消的统一传播、query deadline 和可注入宿主接口属于阶段 2；本次测试只证明进入 Analyze 前已取消的情况，不能宣称整个流水线已实现统一取消。
- CLI 已在 `Run` 中 defer `commandanalysis.CloseBridge()`；生产路径没有第二套分析实现。同步了 AGENTS.md 和搬迁遗留注释。

## Linux 失败的对照与修正

在独立 `/tmp` checkout 回放迁移前 `c502ea2` 的 app 测试，当前初次失败的 10 个顶层测试全部同样失败；另独立复测 tooling 的 safe-dir 写入测试，也重现相同缺少 grant 错误。没有为这些失败修改生产分类/授权逻辑。

逐项处理：

| 失败来源 | 测试调整及保留的约束 |
| --- | --- |
| 3 个非 TTY PowerShell 子例、PowerShell Git origin | 非 Windows 明确 skip；实际 bridge 实现只支持 Windows，不用 mock 声称真实解析通过 |
| 标量统计循环包含 awk、动态 find pattern、单独 awk | 按当前未建模形式锁定 unknown/ask；保留 scalar 不进入 DynamicTargets 和路径展示断言。这是现有保守限制，不是本次新增支持 |
| 前缀环境赋值、read builtin 绑定 | 保留 unknown；仍断言环境目标不能按继承值物化。已知目录不能证明调用效果 |
| 显式信任 find 后又期待 find -delete 是 write | 同时测试显式信任为 read/EvidenceTrust，以及移除该信任后静态识别为 write；外部重定向仍为 write |
| 时间戳 cp、Git commit | 保留目录物化及显示断言；增加 coverage incomplete、不提供规则、已有目录规则不能复用的断言，取代过时的“可保存规则”期望 |
| tooling safe-dir 写 | 先断言无 grant 拒绝且零落盘，再 PrepareAccess → ClassifyAccess 的安全临时目录 allow → exact-call grant → 实际写入 |

没有删除原命名测试，也未改变 cases.json、Windows 事实或工作量快照。测试修正与结构迁移的行为差分分别记录，不能把更保守的测试期望称为误报改善。

## 最终验证

环境：Linux amd64，Go `go1.27.3`，Git `2.56.0`。

通过：

```text
go build ./...
go test ./internal/commandanalysis ./internal/approval ./internal/app ./internal/cli ./internal/tooling -count=1
go test ./... -count=1
go test -tags commandtrace ./internal/app ./internal/commandtrace -run '^(TestCommandAnalysis(Baseline|WorkBaseline)|TestCountersAreOptIn)$' -count=1
git diff --check
```

根模块全量测试使用非缓存执行。沙箱禁止本地监听端口，并在 `/tmp` 暴露 `.git` 占位，影响非仓库 fixture；最终测试经授权在沙箱外运行。

固定版本 Go Task 的 check 命令在工具自身编译时失败：`google.golang.org/grpc@v1.79.3/internal/transport/handler_server.go:271:18: undefined: http2.TrailerPrefix`。没有修改产品依赖来修复工具 runner；依据 Taskfile，直接执行等价的 build/test，其中 test 额外加 `-count=1`。不将 Task runner 记为通过。

Linux 基线执行 17 个 POSIX 和 6 个 argv 用例的独立语义断言；8 个真实 PowerShell 用例跳过。Linux 尚无事实/工作量快照，测试明确报告缺少 measured work baseline；带计数入口通过不等于计数与旧版本相同。未录制新快照。

未执行：最新 Windows 真实 bridge/31 条快照、junction、Windows CLI 生命周期、PowerShell 5.1、macOS、race、provider integration、UAC、远端 push。历史 Windows 验证范围仍按 Handsoff.md 第 6 节记录，不能替代最新验证。

## 兼容层与后续入口

1. `approval/command_compat.go` 的分析函数、事实/枚举别名、CloseBridge 门面：调用者迁移完成后删除，保留工具中立授权类型。
2. `CommandAssessment.AccessIntent` 和 app 的 legacy 结果强制转换：阶段 4 建立角色化目标映射后删除旧映射。
3. `Result = CommandAssessment`、KnownDirs/StaticRead 等 legacy 字段：阶段 3/4 生成并切换目标角色时收敛。
4. `ShellInput.InvocationDialect`、重复静态/外部路径/Git 解析：阶段 2 改为共享 ParsedCommand，移除保留旧方言漂移的过渡字段。
5. `Engine.ResolveGitPushOrigins/ResolveGitPushArgvOrigins` 和 app 测试包装：阶段 2 改为统一 Analyze/query seam 后清理。
6. `Dependencies.UntrustedProgramDirs` 之外的宿主直接访问：阶段 2 引入窄查询接口、上下文快照和总预算。

阶段 2–6 尚未实施；没有统一 catalog、角色化目标或误报下降结论。后续先补 Windows 最终回放，按阶段授权继续，不重跑一次性迁移脚本。
