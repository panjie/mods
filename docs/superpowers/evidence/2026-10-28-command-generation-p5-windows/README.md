# P5 Windows 验收证据

日期：2026-10-28，Asia/Shanghai。基线 `2c7665b9cfcc4153cc1e41b638545200b70f4143`，开始时与实际远端 `origin/main` 一致。P3 `952d02a`、P4 `b906e77`、P5 `2c7665b` 均在历史中。初始工作区仅有用户已有未跟踪 PNG，本轮未操作该文件。

**结论：默认继续 `off`。** Windows 上观察到了自动重组的局部有效样本，但尚不足以证明跨宿主稳定净收益。人工 UI 项目由用户明确暂缓，P5 整体仍未完成。未修改生产提示词、候选门槛、分析器、授权、默认值或评分口径。

## 环境与范围

| 项目 | 实际值 |
| --- | --- |
| 仓库 | `C:\Users\panjie\dev\mods` |
| Windows | Windows 11 Enterprise，10.0.22631，amd64 |
| Go / Git | Go 1.26.3 windows/amd64；Git 2.55.0.vfs.0.10 |
| PS5.1 | `C:\WINDOWS\System32\WindowsPowerShell\v1.0\powershell.exe`，5.1.22621.7649 |
| PS7 | `C:\Program Files\PowerShell\7\pwsh.exe`，7.4.14 |
| CGO / gcc | CGO_ENABLED=0；PATH 未找到 gcc；race 未执行成功 |
| DeepSeek key | 已设置；未输出或写入证据 |
| 请求参数 | `https://api.deepseek.com/`，`deepseek-v4-flash`，temperature 0，max_tokens 4096，think false |
| 实际模型身份限制 | 固定名称的预检请求返回 HTTP 200，但响应 `model=deepseek-flash`；客户端未替换模型，无法证明服务端底层权重身份 |
| 权限/输入 | 非交互 auto、临时合成夹具、无保存规则/项目指令/安装技能/MCP/Web search |
| A/A1 | 独立 `git archive d34fe20`，未在 HEAD 伪装历史分析器 |

PS5.1 lane 从子进程 PATH 移除所有含 `pwsh.exe` 的目录；真实 host 见 `host.json`、`ps51/host.json` 和 reliability 的 host/grammar 测试。两条 lane 独立保存，主目录 `raw/` 为 PS7，`ps51/raw/` 为 PS5.1。

每个宿主均为 A/A1/B/C × normal/minimal × 每场景 5 次：首轮每组/模式 70 次（14 场景），完成实验每组/模式 20 次（4 场景），主对照总计 1,440 次场景运行。另有 1 次无工具模型可用性请求，不计入评测样本。场景运行数不等于 HTTP 请求数；原 harness 未记录实际 HTTP 请求总数。

初次 A/A1 runner 将 Git SHA 错填为 `MODS_GENERATION_BASELINE`。旧 harness 的该变量实际选择冻结提示（仅接受 `original`/`p1`）；两个宿主的 A 各 70 个子测试在 API 前失败，A1 各发出了 70 次请求。**这 140 次额外 A1 结果全部保留**在 `attempts/invalid-a1-selector/{ps7,ps51}/`，按参数配置失败单列，未按结果选择样本；主对照在修正为与 Linux 相同的 `original` 后完整重跑。A/A1 的归档源码始终未改动。总真实模型场景运行数为主对照 1,440 + 额外 140 = **1,580**，另列预检 1 次。

模型矩阵结果与原始文件完整性由 `audit.py` 最终核验。两个宿主都使用原始预登记任务和 oracle；含糊代码统计的正确性仍为 null，不猜测评分。首轮不会调用 `CallTools`，首轮 `automatic_reorganizations=0` 不表示候选无效。

## 已完成的 B/C 结果

下表每行 20 次完成实验，其中 15 次有数值 oracle、5 次含糊任务正确性 unavailable。零审批成功要求正确且没有 `approval_unavailable`，不把任务失败算成零审批成功。

| Host | 模式 | 组 | 正确/15 | 零审批正确/15 | approval_unavailable | 自动重组 | 调用 | 轮次 |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| PS7 | normal | B | 11 | 11 | 9 | 0 | 107 | 63 |
| PS7 | normal | C | 10 | 10 | 10 | 0 | 74 | 47 |
| PS7 | minimal | B | 14 | 13 | 4 | 0 | 53 | 42 |
| PS7 | minimal | C | 12 | 12 | 5 | 0 | 68 | 50 |
| PS5.1 | normal | B | 10 | 10 | 10 | 0 | 111 | 64 |
| PS5.1 | normal | C | 13 | 12 | 8 | 2 | 84 | 56 |
| PS5.1 | minimal | B | 12 | 11 | 5 | 0 | 66 | 48 |
| PS5.1 | minimal | C | 14 | 13 | 3 | 1 | 53 | 43 |

这是分层观察，不做显著性或因果提升声明。PS7 无重组，B/C 差异来自不同生成轨迹，不能归因于 once；更少调用也可能来自更早终止或不同的含糊任务失败路径。PS5.1 有局部有效事件，但每格 5 次、两个模式和宿主表现不一致，尚不能据此改变默认。跨宿主的差异同样包含模型生成差异，不能单独归因为宿主实现。

PS7 minimal / B / physical-lines / repeat 4 有 `tool_errors=1`、`allow_path_errors=1`，最终输出错误的 5 行。其轨迹包含普通审批阻断、文件读取与无关媒体探测。harness 只保存汇总错误数，未保存逐调用返回错误或可信 dispatch，故不能确定是哪次调用失败，更不能把这一计数称为实际进程执行失败。其余 B/C 完成报告这两个字段均为 0。

### 三次真实自动重组

均为 PS5.1 / C / `physical-lines`，计数来源为生产 caller 返回的本地 typed error 经 `ObserveReorganization` 记录，不从普通工具 JSON 文本识别。

| 模式/重复 | 原候选 | 后续 | 结果 |
| --- | --- | --- | --- |
| normal/2 | 字节计数循环，nested_control_flow + stateful_aggregation；此前已有一次普通审批阻断 | 替代 .NET/regex 命令仍 unknown，再次审批不可用 | 六轮上限，失败，2 次 approval_unavailable |
| normal/4 | `$f`/`$lines` + foreach 累加器，stateful_aggregation | 改为两个已证明读取的 Measure-Object 管道 | 正确 `{files:3, lines:6}`，零审批，3 调用/2 轮 |
| minimal/5 | 同类多局部状态累加器 | 改为两个已证明读取的管道 | 正确 `{files:3, lines:6}`，零审批，3 调用/2 轮 |

保留 `internal/app/testdata/command_generation/p5_windows_reorganizations.json` 的全部 shell 序列及来源索引。新 `TestCommandGenerationP5WindowsObservedReorganizations` 使用真实生产解析/授权和计数器 registry：原候选为 typed `not_executed`、保留 replay call ID、IsError、零 mock dispatch；unknown 替代仍 approval_unavailable；只读替代重新分析/授权后才 mock dispatch。两个真实宿主均复跑。没有执行原始未知脚本。

**证据边界：**真实会话有 typed 自动计数，但没有逐调用可信 dispatch 事件或原始 call ID 记录。重放使用明确标识的合成 call ID；mock dispatch 数不是原始模型会话的实测进程数。生产控制分支在 registry dispatch 前返回，重放验证该路径；真实 execution_failures 仍为 unavailable。

## 验证与修正

| 检查 | 结果/证据 |
| --- | --- |
| `go build ./...` | 通过，`verification/build.log` |
| `go test ./...` | 通过；部分包命中 Go 缓存，完整输出保留在 `verification/go-test.log` |
| 请求的四包 P4 定向 `-race` | 退出码 2：`go: -race requires cgo`；不是测试断言失败，也不是 race 通过 |
| commandtrace work baseline | 通过；历史快照未更新 |
| P5 manifest | 通过；B/C fingerprint 保持 `885aba0c661519d48c329d7e94283b84bb02a22c25430930c6b84e4b0f728e7d` |
| P5 接线初跑 | 失败：测试硬编码 POSIX for/do 给 Windows PowerShell，得到 approval_unavailable |
| 接线修正 | 仅换成既有 `organizationCommand()` 平台夹具，PS7/PS5.1 均通过；未改生产规则 |
| 固定 PowerShell 聚合语义 | PS7/PS5.1 均通过，验证物理行数 6、Measure-Object -Line 为 4、CSV 总额 10 |
| P3/P4 非 race 定向 | 两宿主均通过，`verification/*-p3-p4.log` |
| Windows reliability quick | 两宿主退出码 0；均缺 UNC，PS5.1 另缺包管理器 shim 条件；production_ready=false |
| Task build | 实际 `go run github.com/go-task/task/v3/cmd/task@v3.51.1 build` 通过，生成 `bin/mods.exe`；本机没有复现 Linux 的包装器编译错误 |
| Task check/test | 未运行包装器；上方直接 Go 命令不记作 Task check/test 通过 |
| 合成 UI launcher smoke | 非交互实际被审批阻断；不算人工验收 |

完整命令、退出码和时间见 `verification/results.jsonl`、`quick-results.jsonl`、`p3-p4-results.jsonl`；初次失败日志和修正后日志分别保留。二进制版本为 `v4.5.0-39-g2c7665b-dirty`，SHA256 见 verification。

## 人工与未验证项

用户明确选择“本轮暂不操作，人工项保持待验收”。[MANUAL.md](MANUAL.md) 与 `manual-ui.py` 已提供实际二进制、固定本地 provider、合成目录、Y/N/S/Ctrl+C、分页、窄终端/长路径/中文和脚本变化一次/持续变化的入口。自动 smoke 和单元模拟不能替代人工通过。

仍未验证：人工 UI/理解成本、Windows arm64、可用 CGO/gcc 的 Windows race、UNC/缺失 shim 环境、macOS、Linux pwsh、更大样本和模型服务端身份。未运行真实模型 never 对照；确定性测试里的 never 使用模拟 dispatch。未执行 UAC 人工测试。

`approval_unavailable` 是非交互授权阻断；`allow_path_errors` 是生产 caller 在预观察 allow 后报错，不能自动算进程执行失败；`tool_errors` 包含校验/分析错误。自动与人工重组使用 typed 事件，真实模型实验的人工重组为 0，人工验收为待执行。这些类别保持分开。

## 文件与复跑

- `environment.json` / `model-preflight.json`：环境、固定请求参数、模型身份限制。
- `raw/{bc,a1}/{normal,minimal}/`、`ps51/raw/...`：全部原始 JSON 和日志，失败/轮次上限样本不删除。
- `run-model-matrix.ps1`：固定参数及宿主隔离；已有日志拒绝覆盖。
- `summarize.py --lane ps7|ps51`：生成各宿主的 summary.json / tables.md，缺失观测保留 null。
- `audit.py`：核验 16 份报告、完整重复格、参数、无意外写入，生成 raw-inventory.json 和 exceptions.json。
- `extract-replays.py`：从实际 typed 事件会话提取 shell-only 重放夹具，生成后需逐项审阅。
- `verification/`：基线、初次接线失败、修复、双宿主检查及重放。

不覆盖 Linux P5 证据，不改写已推送的 P3/P4/P5 历史；本轮仅本地提交，不推送。
