# Mods 写目标审批设计

**日期：** 2026-09-03  
**状态：** 已实现  
**取代：** `2026-07-02-unified-directory-approval-design.md` 中的读审批矩阵，以及 `2026-08-27-prompt-intent-approval-design.md`

## 目标

审批只保护可产生持久副作用的操作。只要工具调用已被静态分析或 LLM
分类为只读，就无条件执行，不因 workspace 边界、动态路径或
`review-mode=always` 再次询问。无法判定读写性质时按未知写操作处理。

写操作按确定性目标授权：本地文件系统使用目录子树，远程资源使用完整
origin。用户的自然语言意图不构成授权，LLM 也不能生成可持久化的目标。

## 审批矩阵

| 操作 | `auto` | `always` | `never` |
|---|---|---|---|
| 已判定只读 | 允许 | 允许 | 允许 |
| 仅写安全临时目录 | 允许 | 允许 | 允许 |
| 已知普通写目标 | 保存规则全部覆盖时允许，否则询问 | 每次询问，忽略保存规则 | 允许 |
| 未知或动态写目标 | 每次询问，仅可 Allow once | 每次询问，仅可 Allow once | 允许 |

空写目标、未知 effect、无法解析的远端都 fail closed。一次调用同时写本地和
远程时，`auto` 必须同时覆盖全部非临时目录和全部远程 origin。

## 目标与规则

`approval.AccessIntent` 可以表示单一或混合读写组，每组携带本地目录和远程
origin。审批策略只消费写组；读组仍保留在意图中，以便文件工具注入外部读取
能力以及调试展示。

- `DirAllow` 只接受明确的 `mode=write`，覆盖目录本身及后代。
- `RemoteAllow` 只接受明确的 `mode=write`，按完整 origin 精确匹配。
- 历史空 mode 和 read-mode `DirAllow` 行可以被迁移、读取和再次保存，但不再
  授权任何写操作。
- 规则保存在当前 session 的 `approval_rules` 中；`--continue` 恢复，新 session
  不继承。本地目录规则保存规范化后的绝对目录，远程规则保存规范化后的
  origin；两者都不以 workspace 为授权边界，因此继续同一 session 并改变
  工具工作目录后仍然有效。workspace 只用于解析调用中的相对路径。
- `always` 不读取规则来放行，也不展示没有实际作用的 Always allow。

远程 origin 统一为 `scheme://host[:non-default-port]`。scheme 和 host 小写，
HTTP/HTTPS/SSH 等默认端口移除，用户名、密码、路径、查询和 fragment 丢弃。
不同 scheme 或非默认端口互不授权。SSH/SCP 形式 `user@host:path` 归一为
`ssh://host`。

## 目标发现

本地路径继续来自文件工具参数、shell/PowerShell 静态分析以及受限的环境变量
物化。远程 origin 只来自确定性来源：

- HTTP/SSE MCP 配置端点；
- shell 或 `process_run` 参数中的字面 URL 和 SSH/SCP 目标；
- 常见 `git push <remote>` 的显式 URL，或通过一次受限、只读且带超时的
  `git remote get-url --push` 查询解析出的 remote alias。

动态 URL、无法解析的 Git remote、stdio MCP 背后的服务都属于未知远端，只能
逐次审批。MCP `readOnlyHint=true` 始终作为只读；缺失或 false 作为可变工具。
HTTP/SSE 可变 MCP 工具可生成 `RemoteAllow`，stdio 可变工具不能生成 Always
allow。

静态分析能证明 effect 时不调用 LLM。静态无法证明时 LLM 只返回 read、write
或 unknown 以及本地目录提示；解析失败、调用失败或非法响应按 unknown/write
进入单次审批。LLM 输出中的远程地址永远不会成为授权依据。

## 执行与展示

审批和运行时路径能力分离：外部文件读取虽然不显示审批界面，仍通过调用
context 注入已规范化的外部目录，使文件工具能实际访问目标。外部写入也使用
同一能力通道，但必须先通过审批或保存规则。

审批面板分别展示本地目录和已脱敏 origin。Always allow 一次保存本次调用的
整组目录和 origin 规则；只要存在任何未知写目标，就不显示该选项。

SQLite `approval_rules` 增加 `origins` JSON 字段，并把它纳入复合主键。迁移使用
replace-table 模式，旧行的 `origins` 置为空数组语义。

## 验证重点

- workspace、外部、空目标和动态目标读取在 `auto`/`always` 下均免审；
- 临时目录写免审，普通写询问，`never` 全放行；
- 目录子树、origin 精确匹配、混合目标全覆盖和旧规则失效；
- HTTP 默认端口、非默认端口、凭据脱敏、SSH/SCP、Git alias 与动态远端；
- HTTP/SSE 与 stdio MCP 的 Always allow 差异；
- LLM 仅作为 effect 回退且异常时 fail closed；
- origins 数据库迁移、session 恢复、session 隔离，以及改变 workspace 后目录
  和远程规则仍有效；
- POSIX、PowerShell 以及项目标准 `check`、`test`。
