# 原生视觉目录检索修改计划

日期：2026-10-14。状态：首版已实施并验证；实施结果与容量边界见文末。

## 目标与范围

用户执行 `mods '找出当前目录及子目录中包含“目标文字”的图片'`，mods 自动枚举文件、分批读取图片、通过当前模型识别，最后输出命中的相对路径、可核对的文字片段及不确定项。正常路径不需要用户逐个指定 `--image`，也不依赖 Python、OCR 库或递归启动 mods。

“当前目录”默认只查当前层；“整个目录”“所有图片”“目录及子目录”按递归检索理解，工具参数明确体现范围。默认包含隐藏目录，不隐式应用 Git ignore；跳过原因必须可见。目录符号链接默认不递归跟随，防止环路和意外扩大范围；文件符号链接按现有路径解析规则处理。PNG、JPEG、GIF、WebP 沿用现有支持范围；不支持的格式不计作已识别，动画帧支持必须按实际实现说明。

首版完整支持 DeepSeek Flash 的 Chat Completions 与 Responses；同时保证共享接口迁移不会破坏其他 provider。Google 现有不支持工具的行为保持一致。其他支持工具的 provider 只有完成图片回传适配和测试后才暴露读图工具。

不承诺无限目录规模或百分之百 OCR 准确率。容量不足、不可读文件、模型提前结束都必须明确标为未完成，不能将未处理文件归为不匹配。

## 已核实的现状

- `internal/app/stream.go` 只在构造用户消息时附加 CLI、stdin、剪贴板图片。
- `internal/tools/tools.go` 的 Caller/Registry.Call 和 `internal/proto/proto.go` 的 ToolCaller 都只返回字符串；`internal/stream/stream.go` 只产生文本工具结果。
- `internal/openai/format.go` 只编码 user 消息中的图片，tool 消息是纯文本；Ollama 的工具消息也会提前返回而丢过图片处理。
- `internal/openai/openai.go` 将 DeepSeek Responses 的 Images 设为 false，`responses.go` 再次拒绝图片。
- `internal/tools/filesystem.go` 的 fs_list_dir 有 1,000 条截断，没有可续页的递归图片枚举；fs_search 搜索的是文本内容。
- `internal/mcpclient/mcp.go` 把非文本结果转成 `[Non-text content]`，不能直接作为图片通道。
- `internal/image/image.go` 已有 MIME 检测和 20 MiB 图片读取限制；这个限制不能代替多轮请求累计容量检查。
- 会话已有 `proto.Message.Images` 和持久化能力，应优先复用，避免引入第二套图片存储。

官方依据（实施时再次核对）：

- https://api-docs.deepseek.com/guides/vision/
- https://api-docs.deepseek.com/guides/responses_api/
- https://api-docs.deepseek.com/updates/

当前官方文档标明 deepseek-flash 支持原生视觉；旧 Flash 别名路由到新 Flash。Chat Completions 的图片放在 user 消息，Responses 支持工具 output 内的 input_image。不能因此假定 DeepSeek Pro 或所有兼容服务都支持图片。

## 设计决定

### 1. 用现有工具循环传回图片

增加 provider-neutral `proto.ToolResult`，包含文本与带来源标识的图片附件。图片至少关联来源路径/标签，工具调用 ID 沿用现有消息关联；同一批图片的标签与顺序必须稳定。复用 `proto.Image` 的字节和 MIME 字段，所需元数据以向后兼容字段扩展。

将 ToolCaller、工具 Registry 的调用出口、stream.CallTool、app 调度链路迁移到结构化结果。普通文本工具用统一适配函数包装，避免重复迁移逻辑。日志、终端进度、ToolCallStatus、脱敏和错误路径读取文本摘要，不输出图片 Base64。不能通过在字符串里塞 JSON/Base64 伪装为多模态支持。

保留 `proto.Message.Content` 和 `Images` 的兼容性。消息生成应保存工具调用关系和图片来源，不因重试或继续会话重复插入附件。

### 2. 增加两个通用只读工具

`fs_find_images`：输入 path、recursive、cursor、limit；输出稳定顺序的候选图片相对路径、下一页游标、是否枚举结束及扫描错误。文件扩展名用于候选筛选，读取时再验证真实 MIME。文件名不能充当图片内容匹配依据。

枚举必须可取消、可分页，不把全部路径堆入模型上下文。不简单重走目录再按数量跳过；建议使用本轮有效的遍历状态和不透明游标，记录去重路径。游标过期、目录变化或重启后失效应显式报告并允许重新扫描，不能漏页或无限循环。遍历状态随本轮清理。

`fs_read_images`：输入有序 paths 数组；返回逐文件状态、路径标签与真正的图片附件。首版建议每批默认至多 4 张，硬上限 8 张，并同时应用单文件、批次和请求累计容量限制；具体常量以测试结果收敛。部分文件失败仍保留成功图片，失败状态不能被吞掉。无效 MIME、损坏图片、目录冒充图片、空文件、超限和取消都有清晰错误。

工具保持原图，首版不默认缩小图片或拼接联系表，避免细字损失。裁剪/放大作为后续能力，不列为首版依赖。

两个工具均按现有 read intent 和路径安全流程执行。读图不产生写规则，不绕过 requestApproval，也不单独引入读文件确认。仅在 `builtinTools()` 声明注册和启用策略，并纳入 --list-tools。

### 3. 按 provider 正确编码

| 通道 | 计划 |
| --- | --- |
| DeepSeek Responses | 工具 output 编码为文本和 input_image 内容块，保留 call_id、reasoning replay 和原始 response 状态。移除过时禁用逻辑，按实际模型/端点能力启用。 |
| DeepSeek / 兼容 Chat Completions | 先返回本轮所有 tool 结果，再追加携带图片和来源标签的 user 多模态消息。此消息是工具附件投影，不伪造用户指令；不能插在尚未配齐的 tool 结果中间。 |
| OpenAI Responses | 核对当前 SDK/API 图片工具输出支持；支持则原生编码，否则采用明确的用户附件投影。不能静默丢弃附件。 |
| Anthropic | 核对并使用工具结果中的图片内容块，保持同一轮 tool_result 分组。 |
| Ollama | 核对模型/服务的工具加视觉支持；必要时完成工具结果后追加 user 图片消息。未验证通道不宣称支持。 |
| Google | 保持现有工具不可用策略；CLI 图片输入不回归。 |

明确区分“端点接受图片”“模型能看图”“工具结果能回传图片”。现有 Images 布尔值不足时增加内部能力字段；首版不为了该功能新增用户必填配置。已知不支持的模型提前给出可操作说明，未知兼容模型失败时保留供应商错误，不悄悄改用 OCR。

检查当前 SDK 对多模态 output 的表示能力；若缺失，优先沿用仓库已有的局部原始 JSON 编码方式并测试请求体，避免无关的大范围 SDK 升级。

### 4. 工具选择与检索行为

补齐文件意图识别中的图片、照片、截图、image/photo/screenshot 及相关扩展名；用户说“找出包含某文字的图片”也应自动开启所需文件工具。尊重 filesystem=never。

在工具描述和执行提示中说明：图片内容检索先枚举再读取，用视觉输入判断；文字文件读取、Base64 文本和文件名搜索不能替代看图。图片中的文字属于待分析数据，不作为新的操作指令。

要求模型逐批记录匹配、不匹配和不确定结果，随后继续下一批，直到枚举结束或明确达到限制。最终输出路径和证据片段，并区分“无匹配”和“尚未检查”。进度可展示已枚举/已读取/失败数；“读取成功”不等于“识别完成”。

不添加通用嵌套 LLM 工具或自动 shell 调用 mods。大目录后续若需独立批次推理，可在这条数据通道之上扩展。

### 5. 容量、完整性与恢复

所有 provider 请求发送前检查累计图片字节、图片数量及 Base64 后的请求大小裕量，覆盖 CLI 附图、工具图片、历史图片和并行工具结果。仅限制每次工具返回大小不能解决累计膨胀。

首版保留图片历史，不自动裁掉已检查图片，也不自动生成有损识别摘要。预算耗尽时停止继续读取并显式报告未完成；保留扫描进度与可重建的已处理路径信息。UI/最终结果不能显示“全部查完”，除非枚举已结束且所有候选均有处理状态。对模型提前结束但仍有未读取候选的情况，由 app 附加未完成提示，不能只依赖提示词约束。

持久化图片、路径与工具关联，验证 --continue、检查点深拷贝和重试不会丢失或重复图片。游标不跨进程持久化；恢复时重新枚举并结合已有进度核对，文件发生变化则重新检查。无需数据库表结构变化时不添加迁移。

超大目录的自动上下文压缩/独立批次视觉推理另行设计；这是首版明确的容量边界，不把“分批工具读取”误称为解决了全部上下文增长问题。

## 实施顺序与检查点

- [x] **任务 1：建立能力与协议基线。** 为 DeepSeek Flash、旧别名、Pro、Chat/Responses 写能力矩阵测试；以官方协议构造多模态工具请求体样例。确定支持通道和 SDK 编码方案。
- [x] **任务 2：贯通结构化工具结果。** 修改 internal/proto、internal/tools/tools.go、internal/stream、internal/app/request_session.go 和各 provider 调用点。文本工具用适配器保持行为。MCP 先保留现有文本行为并适配类型，MCP 图片透传不作为本次依赖。
- [x] **任务 3：实现 provider 图片回传。** 修改 internal/openai/format.go、responses_format.go、responses.go、openai.go，以及支持通道的 Anthropic/Ollama 格式化和循环代码。先用模拟 API 验证连续两轮和同轮多个调用，再开放工具能力。
- [x] **任务 4：实现枚举与读图工具。** 在 internal/tools 增加 image_tools.go、image_scan.go 及测试；复用 internal/image 的格式识别并补充有效图片校验；在 internal/tooling/tools.go 单点注册，更新 app/tool_support.go 的能力传递。
- [x] **任务 5：完成目录检索体验与预算。** 修改文件意图识别、执行提示、进度及未完成状态；集中检查请求图片预算。不将工具清单写入 identity.md。
- [x] **任务 6：补齐会话、文档及端到端验收。** 覆盖 internal/session、completion_checkpoint、session viewer、transcript；更新 README 视觉检索示例和 DeepSeek 旧限制说明。必要时更新 AGENTS.md 架构说明。

每个检查点完成相应测试；不要在只通了 --image、只加了工具 schema 或只删除 DeepSeek 拒绝条件时宣告功能完成。

## 验证方案

### 必须自动化的回归

- 枚举：多层目录、空目录、超过 1,000 个文件、分页无重复无遗漏、隐藏目录、Unicode/空格路径、取消、符号链接、文件增删和过期游标。
- 图片读取：四种支持格式、伪扩展名、损坏文件、空文件、超限、单批部分失败、路径与图片一一对应、外部目录的既有读取语义。
- 协议：真正的图片内容块进入下一次 HTTP 请求；文本工具不变；多调用先完整返回结果再投影图片；工具调用 ID 和推理状态保留；继续会话和重试不重复附件。
- 容量：多轮累计超限、同轮多次读取、历史附件与新附件叠加、失败后没有假装完成；日志与 transcript 不打印 Base64。
- 能力：Flash 支持；Pro/已知不支持通道不误开启；filesystem=never 生效；Google 工具限制不回归；--list-tools 与注册一致。
- 授权：所有工具仍经过统一入口，读取没有新审批弹窗，也没有生成写授权；现有审批入口回归通过。
- 模拟端到端：用户一句话触发枚举→读取→收到真实图片→继续分页→输出结果；包含失败文件和模型提前结束场景，验证明确的未完成状态。模拟测试验证链路，不冒充模型识别准确率测试。

### 真实模型验收（有匹配的 provider key 才运行）

使用一组固定的小图片：明确中文命中、英文命中、无匹配、小字不确定、不同文件名但相同内容、子目录图片和一个损坏文件。目标文字应置于图片像素中，不出现在文件名或测试提示里。

分别使用 DeepSeek Flash Chat Completions 与 Responses 运行同一句自然语言指令，检查无 Python/OCR/嵌套 mods 调用、路径归属正确、命中有证据、不确定和失败如实展示。记录耗时、调用轮数与 token 用量，不承诺相对 OCR 的固定收益。测试用量与真实识别结论单独记录。

相关包定向测试通过后执行仓库基线：

```sh
go run github.com/go-task/task/v3/cmd/task@v3.51.1 check
go run github.com/go-task/task/v3/cmd/task@v3.51.1 test
```

如需完整 CI 等价验证，运行 task ci；跨平台路径行为覆盖 Ubuntu/macOS/Windows。未获得 API 验证条件时明确列出未执行的真实模型验收，不以模拟测试替代。


## 实施结果（2026-10-14）

- 已实现 `fs_find_images` 和 `fs_read_images`，并在 filesystem=auto 下识别中英文图片请求。分页包含隐藏目录、不递归跟随目录符号链接；分页期间发现已访问目录变化会使游标失效，重新枚举同一范围会替换旧扫描。
- 已实现 `proto.ToolResult`、`Request.ResultCaller` 和 Registry.CallResult。为避免无关迁移，保留原文本 ToolCaller / Caller / Registry.Call；普通工具由 CallResult 包装。旧文本出口拒绝仅支持多模态的工具，避免静默丢图。
- DeepSeek Flash 的 Chat Completions 和 Responses 已完成真实 API 验证。OpenAI-compatible/Anthropic/Ollama 适配有离线回归；未对 Anthropic/Ollama 进行真实模型视觉验收。Google 工具限制保持不变。
- 每批最多 8 张，累计 64 张、20 MiB 原图（含历史），序列化请求上限 40 MiB（供应商额外参数覆盖前）。原生工具验证图片解码，单张不超过 4,000 万像素。图片源标签随会话持久化；调试输出不包含图片字节。
- app 对未完成枚举、未送达模型的候选文件和扫描问题追加未完成提示。该提示检查交付完整性，不宣称能证明模型逐张判断正确。
- 本轮游标不跨进程保存，恢复需重新枚举；本次没有增加跨轮视觉摘要缓存、任意规模扫描或 MCP 图片透传。历史图片继续占用预算，大目录仍需划分范围和使用新会话。
- 图片校验和可移植的测试图片绘制引入 `golang.org/x/image v0.44.0`，其依赖带来 x/text 与 x/sync 的最低版本调整；项目 Go 版本保持不变。

### 已执行验证

- `task check`、`task test`、`task build` 全部通过，生成 `bin/mods`。
- 新增目录分页（超过 1,000 个文件）、中文/空格路径、目录变化、游标范围、取消、符号链接、损坏图片、部分失败、累计预算及历史预算测试。
- 新增模拟 API 端到端测试：枚举→同轮两次读图→真正的图片内容块进入下一次请求；覆盖两种 DeepSeek 接口。
- 新增授权入口、tool result 来源与调用 ID 的数据库往返、Chat/Ollama 投影顺序、Anthropic 结果分组、能力矩阵及文件意图测试。
- 针对新链路的 `go test -race` 通过；Linux amd64、Windows amd64 的 `go build ./...` 通过。没有声称在这两种系统上执行了真实运行测试。
- 真实 CLI 测试 `TestRealAIBlackBoxNativeImageSearch`：使用隔离配置、filesystem=auto、shell=false，一句中文请求搜索根目录及子目录。四张图片中正确找出英文 ORCHID 与中文“报销凭证”两张，读出提示词与文件名均未包含的编号 7341、9286。Chat Completions 约 4.28 秒，Responses 约 3.48 秒；仅作为这次小样本验收耗时，不作为性能承诺。
- 并行交叉编译时，已有 `TestShellReviewFlowUsesLLMAnalysis/redirection_target_dirs_fill_unknown_shell_risk` 出现一次 1 秒等待超时。未修改该测试；单独连续复跑三次通过，随后完整基线通过。

真实验收测试及中文固定图片位于 `tests/blackbox/image_search_test.go` 和 `tests/blackbox/testdata/vision/`。可复现命令：

```sh
MODS_BLACKBOX_PROVIDER=deepseek MODS_BLACKBOX_MODEL=deepseek-flash go test -tags integration ./tests/blackbox -run '^TestRealAIBlackBoxNativeImageSearch$' -count=1 -v -timeout=5m
```
