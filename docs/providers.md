# Provider Details

Mods supports the providers below out of the box. The interactive `mods --config`
wizard handles provider, model, and API key selection; manual configuration lives
under `apis.<provider>` in `mods.yml`.

| Provider     | `--api` value  | Env var                      | Get a key                                     |
|--------------|----------------|------------------------------|-----------------------------------------------|
| OpenAI       | `openai`       | `OPENAI_API_KEY`             | [platform.openai.com][OpenAI]                 |
| Anthropic    | `anthropic`    | `ANTHROPIC_API_KEY`          | [console.anthropic.com][Anthropic]            |
| Google       | `google`       | `GOOGLE_API_KEY`             | [aistudio.google.com][Gemini]                 |
| Azure OpenAI | `azure`        | `AZURE_OPENAI_KEY`           | [azure.microsoft.com][Azure OpenAI]           |
| DeepSeek     | `deepseek`     | `DEEPSEEK_API_KEY`           | [platform.deepseek.com][DeepSeek]             |
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
[OpenRouter]: https://openrouter.ai/settings/keys
[Ollama]: https://ollama.com
[GLM]: https://open.bigmodel.cn/
[Qwen]: https://dashscope.console.aliyun.com/
[Kimi]: https://platform.moonshot.cn/
[MiniMax]: https://www.minimaxi.com/

## Endpoint Selection

Direct requests to `api.openai.com` use the Responses API by default for every
model and feature; set `endpoint: chat-completions` on a model to override.
Mods sends `store: false` and keeps the encrypted response items needed for
stateless reasoning and tool continuation in its local session data. Azure
OpenAI, custom base URLs, and other OpenAI-compatible providers continue to use
Chat Completions.

Official `deepseek-flash`, `deepseek-v4-flash`, `deepseek-v4-flash-vision-exp`,
and `deepseek-v4-pro` requests to `api.deepseek.com` use the
[DeepSeek Responses API](https://api-docs.deepseek.com/zh-cn/guides/responses_api)
automatically. Mods uses its plaintext reasoning events, hosted web search, and
the Codex-compatible free-form `apply_patch` tool. DeepSeek Responses is
stateless, so complete response items and tool outputs remain in the local
session and are replayed on the next turn. DeepSeek Flash accepts image
attachments and images returned by tools on both Responses and Chat Completions.
DeepSeek Pro does not accept image input.

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
  `ollama`, or `azure`).
- `provider-profile` chooses provider-specific semantics on an OpenAI-compatible
  transport (`openai`, `deepseek`, `qwen`, `glm`, `kimi`, or `minimax`). A
  model-level value overrides the provider default.
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
