# 阶段 5 首批命令族验收

日期：2026-10-25。实现阶段 5 的首批审批改善与保守边界；阶段 6 未开始。第四、五阶段按用户要求合并到同一本地提交，包含两个阶段的代码、测试、快照和交接文档；未推送。

## 支持范围与证据

| 单元 | 本批支持 / 修正 | 保留询问或后续项 |
| --- | --- | --- |
| 文本（uniq revision 2） | 数字参数 `--skip-fields=N`、`--skip-chars=N`、`--check-chars=N` 与 `-fN/-sN/-wN`；独立参数值也验证数字；显式第二操作数仍为输出 | 缺值、负数、未知选项、第三操作数；不扩大 sed 脚本或 xxd 参数范围 |
| 普通文件写（revision 3） | touch/mkdir/rm/rmdir/tee/unlink 的显式选项表；`-- -filename` 保留真实写操作数；unlink 只接受单目标 | 未知选项、rm 递归、rmdir -p、touch -r/-t 等未审计有值参数保持 incomplete；组合短选项尚未增加 |
| cp/mv（revision 3） | 恰好两个操作数，可有 `--`；源为单链接普通文件；目的为普通文件、缺失叶子或现有目录中的同名叶子；Linux/Darwin 文件身份接口证明同文件系统且无路径链接 | 递归、backup、符号链接、硬链接、特殊文件、缺失父目录、跨文件系统、源目的相同、`..`、尾随斜杠、动态操作数；Windows 身份证明未实现 |
| Git 前序上下文 | cat/head/tail/wc/echo/uniq/xxd 的内建只读事实，在无重定向、赋值、嵌套或后台执行的简单顺序 AST 中不污染后续 push 解析；保留已有 -C 解析 | cd、printf -v、未知命令或用户信任不构成上下文稳定证明；全局 -c、hook/helper 仍不完整；origin 已知不授权 push |
| wrappers | env 与 find 单次 `-exec … ';'` 复用新增 uniq 参数；find 批量 `+` 仅接受操作数数量不改变只读性质的 NameOnlyRead 条目 | xargs uniq、find 批量 uniq/xxd、动态程序或未知内层调用不继承只读；没有扩大 wrapper 写入授权 |
| PowerShell | 保持 LiteralPath/Value/Encoding 的现有标量事实，新增合成 IR 回归确认文件重定向和 stop-parsing 保留 gap | 本批不扩大 cmdlet 参数或重定向自动放行；真实 bridge、数组/splatting/provider 全组合验收仍需 Windows，不能用 Linux 合成 IR 冒充 |

语义依据：[GNU uniq 参数](https://www.gnu.org/s/coreutils/manual/html_node/uniq-invocation.html)、[GNU cp](https://www.gnu.org/s/coreutils/manual/html_node/cp-invocation.html)、[GNU 目的目录规则](https://www.gnu.org/software/coreutils/manual/html_node/Target-directory.html)。支持集故意小于命令完整语法，不依赖未知参数的猜测。

`TransferResolver` 是可选、有界只读依赖，不执行命令。每次 Analyze 都重新查询；不支持该接口、文件类型变化或查询失败时不得 complete。复制源是 read；移动源和目的都是 rename-entry write。证明成功只更新该 invocation，不能清除组合中的未知调用、cwd 或动态目标 gap。此项沿用已有执行前复核，不宣称消除了文件系统 syscall 竞态。

## 固定样本变化

保留 `cases.json`、旧平台快照与 `baseline_stage4_linux.json`。新的 `baseline_stage5_linux.json` 单独记录本批结果，原有独立 safety 断言未修改。

23 条 Linux 样本中只有两条事实发生变化：

| 样本 | Auto 前 → 后 | 目标与 gap |
| --- | --- | --- |
| posix-copy-mixed | Ask → Allow | 源 read、目的 write；有目的规则；普通文件证明令 coverage complete，移除两个旧缺口 |
| posix-move-source-ungranted | Ask → Ask | 新增源、目的两端完整 write 路径；coverage complete，但缺少源规则，继续询问 |

其余 21 条事实一致；Always/Never 结果不变；固定集 Allow→Ask 为 0。新增独立负例确认 unknown 参数、递归、链接、未知兄弟调用等不能借用目标规则。旧 catalog 的 1,032 条迁移观测未重录：显式列出 unlink 多操作数收紧和六个文件命令的 `-- -output` 绑定修正。

本机暖启动固定样本 benchmark（100 次/样本，Linux amd64）：cp 181,268 ns/op，mv 172,415 ns/op，uniq read 91,698 ns/op，普通 cat read 164,369 ns/op。此次 benchmark 与检查任务并发，只有单次观测；阶段 4 未保存可比耗时，前值缺失，不能声称性能提升或量化耗时差异。审批差异来自保存的阶段 4/5 事实，不能解释为真实用户误报率。

## 验证

- `go build ./...`、`go test ./...`。
- `go test -tags commandtrace ./internal/commandanalysis ./internal/app ./internal/commandtrace`；原 stage2 work 快照未变。
- `go test -race ./internal/commandanalysis ./internal/app ./internal/approval`。
- Windows amd64 根模块、commandanalysis/app 测试交叉编译；不等于 Windows 运行通过。
- `TestStage5TransferApprovalDispatch` 经过生产事实→审批→复核→dispatch：隔离目录真实 cp、mv、uniq；拒绝例 spy 零调用。仅目的授权的 mv 被拒，双端授权成功。
- `TestStage5TransferProof` 覆盖文件/目录目的、链接、硬链接、缺失路径、参数、组合上下文；存在不同设备的 `/dev/shm` 时只读验证跨卷拒绝。
- `TestStage5TransferProofIsFreshAndOptional` 覆盖复核时目的变为链接，以及依赖不提供文件类型证明。
- `TestStage5TextAndWrapperGrammar`、`TestStage5FileGrammar`、`TestStage5GitPredecessor`、`TestStage5PowerShellRedirectionRemainsIncomplete` 分别固定各族边界。

后续优先项：真实 Windows/macOS 执行与平台快照；PowerShell 参数绑定及可靠重定向；有值文件选项、组合短选项；更多 Git 全局选项与受限 wrapper 写入。未审计形式继续普通审批，不为减少 Ask 而推定 complete。
