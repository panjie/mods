# OpenCode Go Responses 路由调查与修改案

日期：2026-10-19。状态：已实施路由、向导、连接检查与文档；真实生成验收受凭据和地区限制。下文保留调查时的现状与方案。

## 结论

Mods 已经实现 OpenAI Responses 的请求、流事件、工具往返和历史续接；OpenCode Go 的问题在于默认路由和配置流程没有使用这些能力。建议复用现有适配器，补齐共享的服务路由元数据、向导保存和连接测试，不另建 Responses 客户端。

不能把 OpenCode Go 整体切换成 Responses，也不能直接套用 DeepSeek 官方服务的路由。网关上的不同模型分别使用 Chat Completions、Responses、Anthropic Messages；当前目录还包含 Google 协议模型。

## 外部证据与验证边界

本次读取了以下公开来源，未发送带密钥的生成请求：

- [OpenCode Go 官方文档](https://opencode.ai/v2/docs/console/go/#endpoints)：当前 Responses 模型为 `gpt-6-luna`、`gpt-5.6-luna`、`grok-4.7`、`muse-spark-1.2-contributor`、`muse-spark-1.3-contributor`。对应生成地址为 `https://opencode.ai/inference/go/openai/v1/responses`。同表中的 Kimi、GLM、DeepSeek 使用 Chat Completions，MiniMax、Qwen 使用 Anthropic Messages。
- [models.dev 机器可读目录](https://models.dev/api.json)：`opencode-go` 下上述五个模型的 `provider.npm` 是 `@ai-sdk/openai`，其 `provider.api` 指向新的 OpenAI 协议根地址；提供商默认包仍是 `@ai-sdk/openai-compatible`。这与官方协议表一致，可作为维护路由快照的交叉证据。
- [旧模型列表](https://opencode.ai/zen/go/v1/models) 与 [新模型列表](https://opencode.ai/inference/go/v1/models)：本次匿名 GET 都返回 HTTP 200，各有 37 条模型。实际条目只有 `id`、`object`、`created`、`owned_by`，没有 endpoint 或协议字段。官方文档说有 metadata，不等于当前响应包含可用的协议元数据。
- `GET https://opencode.ai/inference/go/openai/v1/models` 本次返回 HTTP 404。因此新的生成 base URL 不能直接追加 `/models` 进行发现。

协议地址来自官方资料；匿名模型列表成功不能证明生成、工具调用或多轮续接成功。旧 `/zen/go/v1` 的生成兼容性、新接口对 `include: [reasoning.encrypted_content]` 等字段的接受情况，仍需真实协议验收。

## 调查时的代码缺口

| 位置 | 现状与影响 |
| --- | --- |
| `internal/providerinfo/provider.go` | 已有 `opencode-go` 与 `opencode-go-anthropic`；默认 URL 仍为 `/zen/go/v1`，没有按模型划分的路由信息。 |
| `internal/app/provider.go: useResponsesEndpoint` | 识别显式 `endpoint`、官方 OpenAI 与官方 DeepSeek。OpenCode Go 的空 endpoint 一律落到 Chat Completions。 |
| `internal/cli/configure.go: fetchModelIDsContext` | 仅取模型 ID，不判别协议；混合目录可能让 Anthropic 模型出现在 OpenAI 提供商中。 |
| `internal/cli/setup_async.go: discover / saveData` | draft 和异步消息已有 `endpoints`，但只有 Copilot 发现填充；保存时字段名仍是 `copilotModelEndpoints`。 |
| `internal/cli/configure.go: buildConfigWizardUpdates` | 只有 Copilot 使用发现的 endpoint；已配置模型整项跳过，单改新增模型逻辑无法修复旧的空 endpoint 条目。 |
| `internal/cli/configure.go: testConnectionContext` | 固定 POST `/chat/completions`，请求体使用 `messages`、`max_tokens`；Responses 模型会被错误测试。该请求也没有 OpenCode 的会话头。 |
| `internal/app/provider.go: buildProviderConfigs` | 正式请求已经按精确主机名发送稳定的 `x-opencode-session` 和自己的 User-Agent，Responses 路径应沿用。 |
| `internal/openai/responses.go` | 已发送 `input`、`max_output_tokens`，处理 SSE 和工具循环。默认 OpenAI profile 还发送 `store: false` 与 encrypted reasoning include，需要确认网关兼容性。 |
| `internal/config/config_template.yml` | 注释已有 `gpt-6-luna: {endpoint: responses}` 示例，但向导没有落实相同默认行为。 |

## 推荐方案

### 1. 集中维护 OpenCode Go 的服务路由

在 `internal/providerinfo/opencode_go.go` 增加小型、无网络访问的路由表，记录精确模型 ID、transport protocol、endpoint，以及官方来源和核对日期。首批覆盖上面五个 Responses 模型，并记录用于发现过滤的已确认 Chat / Messages 模型。

提供共享的查询函数，供运行时、向导和连接测试使用。查询必须同时核对服务地址：HTTPS、精确主机 `opencode.ai`、已知 Go 路径；不只看提供商名称，也不使用 `gpt-*`、`grok-*` 等宽泛前缀。旧 `/zen/go/v1`、过渡 `/inference/go/v1` 与新 `/inference/go/openai/v1` 分开列为已知 Go 根路径；识别旧地址不代表自动改写它。

这张表描述服务的协议路由，不描述模型的图片、音频、视频能力。不能因模型叫 Qwen 就在网关上自动启用 qwen profile 或媒体能力。

第一版不在每次请求时拉取 models.dev，也不把第三方目录的 URL 直接用于携带密钥的请求。可后续增加开发期同步脚本，通过审核更新内置快照。

### 2. 运行时为旧配置提供默认路由

`useResponsesEndpoint` 在显式 endpoint 判断之后，查询共享 OpenCode Go 路由。优先级为：

1. 用户明确配置的 `endpoint`。
2. 官方服务地址和精确模型 ID 命中的内置路由。
3. 现有默认规则。

因此，已有 `gpt-6-luna: {}` 条目也能使用 Responses；已有 `endpoint: chat-completions` 保持用户选择。同名提供商指向其他网关时不套用 Go 规则，自定义提供商别名指向已知 Go URL 时可使用相同规则。

未知模型保留显式配置入口；向导标记“协议尚未确认”，要求选择路由或使用手动配置，不把它标成已确认兼容。运行时保持原有默认行为，不增加隐藏网络探测，也不遇到 400/404 就跨协议重试，以免掩盖鉴权、限额或字段错误。

### 3. 让发现、选择和保存保留协议信息

将 `copilotModelEndpoints` 改为通用的 `modelEndpoints`，保留 Copilot 的现有选择逻辑。OpenCode 专用发现分支读取统一模型目录，再用共享表生成路由信息：

- OpenAI 提供商展示 Chat Completions 和 Responses 模型，并标注 endpoint。
- Anthropic 提供商展示已确认 Messages 模型。
- 未知协议单独提示，不能静默宣称支持；Google 模型不能混入这两个提供商。Google Go 接入另行处理。
- 新增模型写入正确的 endpoint；已有模型只补缺失的 endpoint 子字段，保留显式选择、别名和其他设置，不能整项覆盖。

异步命令只返回结果；仍由 `receive` 按 request/provider 版本检查后更新 draft。取消、切换提供商和迟到消息继续沿用现有防护。

### 4. 连接测试使用将要保存的实际路由

连接检查先根据当前配置与 draft 形成有效的 provider/model 路由，再构造对应请求，避免第二套独立判断：

| endpoint | 最小请求形状 |
| --- | --- |
| Chat Completions | `/chat/completions` + `model`、`messages`、输出上限 |
| Responses | `/responses` + `model`、`input`、`max_output_tokens`、`store: false` |

尽量复用现有适配器的构造与流解析，使用有界输出，要求读到正常终止事件；HTTP 200 或任意非空正文不能单独算通过。输出不足导致 `incomplete` 时需区分于成功终止。优先检查默认模型，不为目录内每个模型自动发生成请求。

OpenCode 检查请求也发送客户端 User-Agent 和该向导会话内稳定的 `x-opencode-session`，共用正式请求的头构造规则；会话 ID 的创建和生命周期仍由调用方管理。发现请求如带这些头，也必须经过相同服务地址判定。凭据使用解析后的实际值，兼顾环境变量和配置存储；网络闭包捕获快照，不直接修改 draft。

403 不应一概显示“invalid API key”：还可能是模型权限或服务策略；显示 HTTP 状态和经脱敏、限长的服务错误。

### 5. 新 URL 与现有用户兼容

新建配置使用官方当前的生成地址 `https://opencode.ai/inference/go/openai/v1`，同时为 Go 专用发现配置统一目录 `https://opencode.ai/inference/go/v1/models`。不能复用通用的 `baseURL + /models`。

Anthropic 新建配置同步采用官方当前的 `/inference/go/anthropic/v1/messages`，并从同一个目录发现后过滤。已有用户的 base URL 不自动迁移；如果旧生成 URL 不再可用，明确提示更新地址。

复用现有 `api-type: openai`、`provider-profile: openai` 和模型级 `endpoint`，不新增持久配置键、CLI flag 或数据库迁移，也不需要第三个 `opencode-go-responses` 提供商。

### 6. Responses 方言兼容性先验证再扩展

当前适配器以 OpenAI profile 发送 encrypted reasoning include。单凭服务支持 `/responses`，无法证明 Grok、Muse Spark 都接受该扩展，也不能证明其输出项可以跨模型重放。

实施时先对 GPT Luna、Grok、Muse Spark 各验证文本、函数工具往返和多轮历史。如果某类后端拒绝这些字段或返回不同续接状态，再基于实际 wire 证据增加最小的服务方言处理；相应修改必须包含请求体与历史重放 fixture。不应提前全局移除 encrypted reasoning，也不借用 DeepSeek profile 绕过校验。

## 现在即可使用的显式配置

下面只展示一个 Responses 模型；同一 provider 下可继续添加 Chat 模型。已有配置请合并字段，不覆盖整个 `apis`。

```yaml
apis:
  opencode-go:
    api-type: openai
    base-url: https://opencode.ai/inference/go/openai/v1
    api-key-env: OPENCODE_GO_API_KEY
    models:
      gpt-6-luna:
        endpoint: responses
```

该片段在调查时已经能由运行时表达，但当时向导的通用发现会遇到上述 404。实施后，向导已经使用统一目录；真实生成是否成功仍需单独验收。

## 实施拆分与验收

1. **共享路由与运行时。** 增加精确 ID/URL 路由测试：五个 Responses 模型、Chat 模型不切换、显式覆盖、空 endpoint 旧配置、自定义别名、非 Go 路径、伪装域名、未知模型。
2. **向导发现与保存。** 使用混合目录 fixture 验证协议过滤、未知模型提示、统一发现 URL、通用 endpoint 结果、旧模型子字段补全与显式配置保留；覆盖取消和迟到结果。Copilot 发现与保存回归通过。
3. **连接测试。** mock server 检查路径、body 和头；覆盖 Responses 正常终止、200 但响应无效、incomplete、流失败、401/403、超时和取消，验证与保存后的路由一致。
4. **适配器与真实服务。** 保留 Responses 请求字段、工具调用、错误终止和历史状态测试。真实验收分别覆盖 GPT Luna、Grok、Muse Spark，至少一次函数调用及连续追问；恢复保存会话后再追问。使用非私密输入，单独记录服务/字段兼容问题，不能以 mock 测试替代。
5. **文档与基线。** 更新 `docs/providers.md`、模板中的内置名称/环境变量/新地址和 Responses 示例。完成相关包测试，再按 AGENTS.md 执行 Task `check` 和 `test`；涉及平台差异的部分由三平台 CI 覆盖。

实施记录：共享路由表、运行时默认选路、统一模型目录和协议过滤、已有模型 endpoint 子字段补全、使用真实适配器的连接检查均已实现。未知协议模型在向导中提示使用手工 YAML 配置；未增加单独的协议选择页面。真实服务验收使用独立的 integration 测试，不混入默认测试。

真实验收命令：`go test -tags integration ./internal/app -run '^TestOpenCodeGoResponsesIntegration$' -count=1 -v`。2026-10-19 结果：GPT 6 Luna 与 Grok 4.7 在新 Responses URL 收到 HTTP 401 / `api_key_expired`；Muse Spark 1.3 Contributor 收到 HTTP 403 / `RegionError`。因此未进入实际生成或工具续接阶段，不能声称三个模型已完成真实兼容性验收，也没有据此改变 encrypted reasoning 等协议字段。

调查阶段已有基线：以下定向测试命令通过，`internal/app`、`internal/cli`、`internal/openai` 三个包均为 `ok`。它验证原有 OpenCode 提供商/会话头、通用模型发现和 Responses 请求/工具续接/流提前结束处理，不包含后续新增的自动选路测试。

```powershell
go test ./internal/app ./internal/cli ./internal/openai -run '^(TestSetupOpenCodeGoProviders|TestBuildProviderConfigsOpenCodeSessionHeaders|TestResponsesRequestWireFormat|TestResponsesToolRoundReplaysEncryptedReasoning|TestResponsesEarlyEndBecomesStreamError|TestDiscoverModelsOpenAI)$' -count=1
```

### 实施后的检查结果

- 新增的官方 URL/精确 ID 路由、覆盖优先级、目录过滤、旧配置保留、环境变量凭据、流终止/错误/取消测试全部通过。
- `go run github.com/go-task/task/v3/cmd/task@v3.51.1 check` 通过。
- `go run github.com/go-task/task/v3/cmd/task@v3.51.1 test`：除 `internal/tools` 中 Git 认证测试外，其余包通过，包括本次涉及的 `internal/app`、`internal/cli`、`internal/openai`、`internal/providerinfo` 和 `internal/config`。
- Git 测试失败由 `GIT_CONFIG_GLOBAL=NUL` 触发，本机 Git 2.55.0.vfs.0.10 报 `fatal: unable to access 'NUL': Invalid argument`。设置相同环境变量后运行独立的 `git -c user.name=ModsTest config --get user.name` 可复现；相关测试与认证实现未被本次修改。
- `golangci-lint run ./...` 仅报告三个未修改文件的 gofmt 问题：`internal/apperr/error.go`、`internal/approval/paths.go`、`internal/approval/readonly_policy.go`，`gofmt -d` 显示为 CRLF 换行差异。本次修改文件的 gofmt、LF 和 `git diff --check` 均通过。
- 真实服务探测结果见上文：密钥过期和地区限制阻止生成，未将其记为验收通过。
