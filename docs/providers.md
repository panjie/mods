# Provider Details

Mods supports the providers below out of the box. The interactive `mods --config`
wizard handles provider, model, and API key selection; manual configuration lives
under `apis.<provider>` in `mods.yml`.

| Provider     | `--api` value  | Env var                      | Get a key                                     |
|--------------|----------------|------------------------------|-----------------------------------------------|
| OpenAI       | `openai`       | `OPENAI_API_KEY`             | [platform.openai.com][OpenAI]                 |
| ChatGPT      | `chatgpt`      | — (browser sign-in)          | [Subscription setup](../README.md#chatgpt-subscription) |
| GitHub Copilot | `github-copilot` | — (device authentication) | `mods --config` |
| Anthropic    | `anthropic`    | `ANTHROPIC_API_KEY`          | [console.anthropic.com][Anthropic]            |
| Google       | `google`       | `GOOGLE_API_KEY`             | [aistudio.google.com][Gemini]                 |
| Azure OpenAI | `azure`        | `AZURE_OPENAI_KEY`           | [azure.microsoft.com][Azure OpenAI]           |
| DeepSeek     | `deepseek`     | `DEEPSEEK_API_KEY`           | [platform.deepseek.com][DeepSeek]             |
| OpenCode Go  | `opencode-go` / `opencode-go-anthropic` | `OPENCODE_GO_API_KEY` | [opencode.ai/auth][OpenCode Go] |
| OpenRouter   | `openrouter`   | `OPENROUTER_API_KEY`         | [openrouter.ai][OpenRouter]                   |
| Ollama       | `ollama`       | — (local)                    | [ollama.com][Ollama]                          |
| GLM          | `glm`          | `ZAI_API_KEY`                | [open.bigmodel.cn][GLM]                       |
| Qwen         | `qwen`         | `DASHSCOPE_API_KEY`          | [dashscope.console.aliyun.com][Qwen]          |
| Kimi         | `kimi`         | `MOONSHOT_API_KEY`           | [platform.moonshot.cn][Kimi]                  |
| MiniMax      | `minimax`      | `MINIMAX_API_KEY`            | [www.minimaxi.com][MiniMax]                   |

[OpenAI]: https://platform.openai.com/account/api-keys
[Anthropic]: https://console.anthropic.com/settings/keys
[Gemini]: https://aistudio.google.com/apikey
[Azure OpenAI]: https://azure.microsoft.com/en-us/products/cognitive-services/openai-service
[DeepSeek]: https://platform.deepseek.com/api_keys
[OpenCode Go]: https://opencode.ai/auth
[OpenRouter]: https://openrouter.ai/settings/keys
[Ollama]: https://ollama.com
[GLM]: https://open.bigmodel.cn/
[Qwen]: https://dashscope.console.aliyun.com/
[Kimi]: https://platform.moonshot.cn/
[MiniMax]: https://www.minimaxi.com/

## Adapter Capabilities

This matrix describes implemented protocols, not what a particular model can
understand. The service decides whether the selected model accepts a tool or
media input. `chat` means Chat Completions; `responses` means the Responses API.
In paired columns, the first value is direct input support and the second is
support for attachments returned by tools. Tool execution always passes through
the same registration, argument validation and approval path.

<!-- provider-capabilities:start -->
| Route | Function tools | Image input | Tool images | Audio input/tools | Video input/tools |
|---|---|---|---|---|---|
| openai / chat | yes | yes | yes | no/no | no/no |
| openai / responses | yes | yes | yes | no/no | no/no |
| custom-gateway / chat | yes | yes | yes | no/no | no/no |
| deepseek / chat | yes | yes | yes | no/no | no/no |
| deepseek / responses | yes | yes | yes | no/no | no/no |
| qwen / chat | yes | yes | yes | yes/yes | yes/yes |
| qwen / responses | yes | yes | yes | yes/yes | yes/yes |
| glm / chat | yes | yes | yes | no/no | no/no |
| kimi / chat | yes | yes | yes | no/no | no/no |
| minimax / chat | yes | yes | yes | no/no | no/no |
| azure / chat | yes | yes | yes | no/no | no/no |
| anthropic / anthropic | yes | yes | yes | no/no | no/no |
| google / google | yes | yes | no | no/no | no/no |
| ollama / ollama | yes | yes | yes | no/no | no/no |
<!-- provider-capabilities:end -->

`TestProviderCapabilityDocumentation` checks this block against the actual
adapters used by `TestProviderToolContract`. OpenRouter and OpenCode Go use their
configured OpenAI or Anthropic adapters. GitHub Copilot uses its selected
Chat Completions or Responses endpoint; its Messages endpoint is not implemented.
ChatGPT subscription uses its dedicated Responses profile and authentication flow.
Those authentication flows have separate tests; local protocol fixtures do not
certify a live account, gateway or model.

Google supports filesystem, command and MCP tool calls with text results.
It accepts direct image input, but tool-result image transport is not implemented;
unsupported tool media is rejected rather than silently removed from requests.
Audio and video support currently requires the Qwen profile.

## Endpoint Selection

The built-in `openai` provider uses Responses by default when its base URL points
to `api.openai.com`; set `endpoint: chat-completions` on a model to override.
Mods sends `store: false` and keeps the encrypted response items needed for
stateless reasoning and tool continuation in its local session data. Azure
OpenAI uses Chat Completions. Custom provider names and gateway URLs default to
Chat Completions unless an explicit endpoint or the official OpenCode Go routing
table selects Responses. The `chatgpt` subscription provider requires Responses.

With the built-in `deepseek` provider, `deepseek-flash`, `deepseek-v4-flash`,
`deepseek-v4-flash-vision-exp`, and `deepseek-v4-pro` requests to `api.deepseek.com` use the
[DeepSeek Responses API](https://api-docs.deepseek.com/zh-cn/guides/responses_api)
automatically. Mods uses its plaintext reasoning events, hosted web search, and
the Codex-compatible free-form `apply_patch` tool. DeepSeek Responses is
stateless, so complete response items and tool outputs remain in the local
session and are replayed on the next turn. Mods can send images through both
DeepSeek protocols; image acceptance is decided by the service, without a local
model-name allowlist.

To opt out of Responses for a model, set `endpoint: chat-completions`:

```yaml
apis:
  deepseek:
    base-url: https://api.deepseek.com/
    models:
      deepseek-v4-flash:
        endpoint: chat-completions # optional explicit fallback
        reasoning-effort: high     # low, high, or max with --think
```

`api-type`, `provider-profile`, and `endpoint` are intentionally separate:

- `api-type` chooses the transport adapter (`openai`, `anthropic`, `google`,
  `ollama`, `azure`, or `github-copilot`).
- `provider-profile` chooses provider-specific semantics on an OpenAI-compatible
  transport (`openai`, `deepseek`, `qwen`, `glm`, `kimi`, or `minimax`). A
  model-level value overrides the provider default. The `chatgpt` profile is
  reserved for the built-in subscription provider. Native adapters also have
  `anthropic`, `google`, and `ollama` profiles.
- `endpoint` chooses `responses` or `chat-completions` for a model on an
  OpenAI-compatible transport.

An arbitrarily named gateway can therefore use DeepSeek Responses explicitly:

```yaml
apis:
  company-router:
    api-type: openai
    base-url: https://gateway.example.com/v1
    api-key-env: COMPANY_ROUTER_KEY
    models:
      deepseek-v4-flash:
        provider-profile: deepseek
        endpoint: responses
```

Unknown enum values are rejected instead of silently falling back. Custom
providers must configure `base-url`; only built-in providers with a known
official endpoint may omit it. `extra-params` must match the selected protocol.

When web search is enabled, an endpoint with hosted search capability uses that
capability automatically. DeepSeek Responses therefore searches without a
Tavily key; other endpoints keep the existing local search behavior.

The Azure adapter uses Chat Completions and does not support
`endpoint: responses`.

GitHub Copilot uses device authentication through `mods --config` and supports
Chat Completions and Responses routing. Models requiring `endpoint: messages`
are rejected because Copilot Messages transport is not implemented.

## OpenCode Go

OpenCode Go serves models through several protocols. Use `opencode-go` for
OpenAI-compatible Chat Completions and Responses models, and
`opencode-go-anthropic` for Anthropic Messages models. Both use
`OPENCODE_GO_API_KEY`.

On official Go endpoints, Mods automatically selects Responses for
`gpt-6-luna`, `gpt-5.6-luna`, `grok-4.7`, `muse-spark-1.2-contributor`, and
`muse-spark-1.3-contributor`. This also applies to existing models with no
`endpoint` setting and custom provider names using the same official Go URL.
An explicit model `endpoint` always takes precedence. Other gateway URLs do
not inherit these defaults from the model name.

```yaml
apis:
  opencode-go:
    base-url: https://opencode.ai/inference/go/openai/v1
    api-key-env: OPENCODE_GO_API_KEY
    models:
      gpt-6-luna:
        endpoint: responses # optional: selected automatically
      kimi-k2.7-code:
        endpoint: chat-completions
  opencode-go-anthropic:
    base-url: https://opencode.ai/inference/go/anthropic/v1/messages
    api-key-env: OPENCODE_GO_API_KEY
    models:
      minimax-m3: {}
      qwen3.8-max: {}
```

The configuration wizard reads the shared `/inference/go/v1/models` catalog
and offers models with a confirmed matching protocol. Unknown models are
listed as unconfirmed; configure their protocol and endpoint manually in
`mods.yml` after checking the [official endpoint table][OpenCode Go endpoints].
Google-protocol Go models require separate configuration and are not offered
under the OpenAI or Anthropic providers.

New configurations use the URLs above. Existing `/zen/go/v1` and
`/inference/go/v1` configurations keep their configured URL and receive the
same model routing defaults. If a legacy URL is rejected by the service,
update it to the current URL. The new OpenAI generation URL does not expose a
`/models` endpoint; Mods uses the shared catalog instead.

Runtime and setup requests identify Mods with its own User-Agent and a stable
`x-opencode-session` ID. The OpenAI-compatible connection check uses the
selected endpoint and requires a normally completed stream. Authentication,
regional availability, and model access remain subject to the service.

[OpenCode Go endpoints]: https://opencode.ai/v2/docs/console/go/#endpoints

## Reasoning Controls

`-t` / `--think` enables thinking where the provider and model support it.

- Anthropic requests use the Messages API. Mods chooses adaptive or manual
  extended thinking for recognized Claude models. During tool use it keeps
  complete thinking, signature, redacted-thinking, and tool-use blocks in local
  session state and sends them back unchanged with tool results. For an opaque
  model on a custom `api-type: anthropic` endpoint, configure `thinking-type`
  explicitly instead of relying on model-name inference.
- Gemini 2.5 uses `thinkingBudget` (`-1` requests a dynamic budget), while
  Gemini 3 maps `reasoning-effort` to `thinkingLevel`.
- Ollama sends its native `think` field and renders `message.thinking`
  separately from the final answer.

Exact effort values remain model- and provider-dependent; unsupported configured
values fail before the HTTP request. Provider-specific settings also control
returned thought fields and inline thought tags. `reasoning-effort-off`
configures model-aware OpenAI reasoning disablement. For Anthropic with thinking
requested, `thinking-type` overrides the inferred mode; `thinking-budget`
applies only to manual `enabled` mode.

```sh
mods --api anthropic --model claude-sonnet-4-6 -t "explain this error"
mods --api glm --model glm-5.2 "把这个日志总结成中文要点"
mods --api ollama --model llama4 "summarize this log"
```

## Custom Providers

`mods --config` can discover models for providers that expose a model-list
endpoint. If discovery is unavailable, enter the model name in the wizard or add
it manually:

```yaml
apis:
  groq:
    base-url: https://api.groq.com/openai/v1
    api-key-env: GROQ_API_KEY
    models:
      llama-3.3-70b-versatile:
        aliases: ["groq-llama"]
```

Credential lookup uses `api-key`, `api-key-env`, or `api-key-cmd`. Prefer an
environment variable so neither the config nor model context contains the
secret. Avoid `api-key` in shell arguments as well, since they may be recorded
in shell history or visible to other local processes.
