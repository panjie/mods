# 命令分析阶段 4：角色化目标与授权适配

日期：2026-10-24 启动，2026-10-25 续作验收。基于 `0ce4129`，按用户“实施第四阶段”授权推进。本轮代码尚未提交或推送。实现及 Linux 验证完成；Windows/macOS 真实运行证据仍待补。

## 目标与授权路径

`Result.Targets` 现在携带资源 kind（filesystem/origin/provider）、read/write/candidate 角色、操作类型、原始操作数、语法节点 ID、词法路径、解析路径、授权目录、解析状态和字面量标记。`Clone` 复制目标切片，避免审批快照受后续修改影响。

catalog 对已知输入/输出给出明确角色：uniq/xxd 的输入为 read、输出为 write；普通双操作数 cp 的源为 read，mv 的源为 rename-entry write；PowerShell 已知 Path/LiteralPath 读取也有 read 目标。尚未建模的参数形式不会因此获得完整覆盖。POSIX 输入/输出重定向保留各自角色和引号语义。Git 仓库及 push 查询的已知写范围单独加入 write 目标；URL/路径字面量 fallback 始终只是 candidate。

`app.commandResultToIntent` 是初次审批和复核的唯一生产转换入口：显式验证 effect/evidence/coverage、目标枚举和解析状态，再生成 split ReadDirs/WriteDirs/ReadOrigins/WriteOrigins。无证据 read、非法组合、缺少写目标、provider 或 unresolved 写都不产生可复用权限。`KnownDirs` 保留历史展示/差分用途，但转换不读取它，也不从 cwd 推断写范围。旧 `CommandAssessment.AccessIntent()` 仅留作 legacy fixtures，生产无调用。

## 操作相关的路径检查

- 内容写跟随叶子链接，授权实际叶子的父目录；不存在的叶子通过已有父路径解析。
- remove-entry/rename-entry 检查目录项父目录，不把被删除链接的指向当成删除目标；中间目录链接仍解析。
- mkdir 的 create-entry 还查询最近已有祖先：`mkdir -p new/nested` 会修改现有祖先以创建 `new`，不能只给尚不存在的 `new` 授权。新增可选只读 `CreationParentResolver`，受查询/输出/深度/取消预算约束；缺乏此证据则 incomplete。
- 删除/重命名的尾随分隔符或 `.`/`..` 形式保守保留缺口；不将其当成普通不跟随叶子的目录项操作。
- 原生 argv 的 `$HOME` 等保持字面含义；POSIX 引号及 PowerShell 单引号也保留字面证据。未知读取参数不会自动形成未知写范围。
- cp/mv 的目标文件/目录、递归、backup、跨卷等语义仍未全部建模，OperationDestination 保持 incomplete。本阶段没有提前开放阶段 5 的普通复制/移动规则。

## 审批与复核

修复路径规范化的一个混合操作边界：非 nil 的空 WriteDirs 必须保留。若它变成 nil，已知 ReadDirs 会使一个未知写操作看起来只有读取。新增真实 toolCaller 测试保证这种情况继续询问，拒绝后零执行。

`AccessIntent.TargetBindings` 保存写目标叶子身份，不授予权限。复核比较写授权范围、完整度、未知部分及绑定；忽略诊断文字、候选排序和自动允许的读取变化，并更新当前读取上下文。同一目录内链接从文件 A 切换到 B 也会重新审批；再次变化直接失败，执行 spy 保持零调用。既有 Git 配置变化复核、程序绑定、原生 filesystem grant、ReviewAlways/ReviewNever、非交互路径继续沿用原机制。

UI 分别展示 Read source、Write target、Candidate 和 unresolved/unknown 部分；链接显示词法路径到实际目标的关系。完整命令及目标行继续走既有分页。读取来源未解析时，不把已知输出错误地显示为未知写。

## 测试与差分

新增分析测试覆盖：输入/输出角色、移动源写角色、内容写与删除链接的区别、中间目录链接、不存在的目标、mkdir 缺失祖先、字面 argv/引号、动态读取、PowerShell 无宿主角色元数据及节点 ID。

新增 app 测试覆盖：

- 完整混合复制事实仅需输出写规则；移动事实还需源目录项规则。生产端到端使用已完整支持的 uniq 证明输入未获写授权也可执行；cp/mv 实际调用继续不完整。
- 无证据 read、零值/非法枚举、错误目录与叶子关系、provider、未知 origin、未知远端和 legacy-only 结果均失败关闭。
- candidate 不进入 WriteDirs 或保存规则；未知程序的已知路径/重定向不借目标获得放行。
- 动态读取来源加确定输出可使用输出规则；未知写集合在重复规范化后仍为写。
- 相同目录不同叶子需要重新审批；持续变化零执行；诊断/候选/读取变化不使写授权失效。
- 实际批准链与拒绝链使用 spy，不执行测试中的任意 shell 命令。

`baseline_stage4_linux.json` 是新的独立观测文件，包含原事实及 ReadDirs/WriteDirs/candidate 路径/意图覆盖度。基线 harness 已改为真实生产 adapter。原 `baseline_linux.json`、Windows 快照与 stage2 work 快照均保留。

23 条 Linux 固定样本 Auto/Always/Never 结果全部不变。原字段只有两处差分：`posix-copy-mixed`、`posix-move-source-ungranted` 新增 `unbounded write targets` 缺口，两者原本就 incomplete/ask。没有用更新快照改变独立 safety oracle。parser/bridge/Git work 快照保持一致。

验证命令：

```sh
go build ./...
go test ./...
go test -tags commandtrace ./internal/commandanalysis ./internal/app ./internal/commandtrace
go test -race ./internal/commandanalysis ./internal/approval ./internal/app
GOOS=windows GOARCH=amd64 go test ./internal/commandanalysis -c -o /tmp/mods-commandanalysis-stage4-windows.test.exe
GOOS=windows GOARCH=amd64 go test ./internal/app -c -o /tmp/mods-app-stage4-windows.test.exe
GOOS=windows GOARCH=amd64 go build ./...
git diff --check
```

继续使用 Taskfile 的等价原生命令，沿用阶段 1/2 已记录的 Task runner 依赖编译问题。没有修改产品依赖来修复 Task runner。

## 限制与后续

尚缺真实 Windows pwsh/5.1、junction/命令复核运行、stage4 Windows 快照和 macOS；交叉编译与合成 IR 不代替这些证据。未执行 provider integration、UAC 或远端 push。路径复核仍不能消除文件系统系统调用竞态，也不提供 shell 沙箱。

阶段 5 才逐族完善参数和普通 cp/mv 的目标类型推断；阶段 6 再清理 legacy 字段/门面及历史测试。当前保留它们不意味着仍有从 KnownDirs/cwd 推导写权限的生产路径。
