> [!NOTE]
> #### Actively Maintained Fork
>
> The original Mods was [sunset by Charm](https://github.com/charmbracelet/mods) on
> March 9, 2026. **This fork** is actively maintained with new features and fixes.
>
> See [What's New](#whats-new) for a list of additions since forking.

# Mods

<p>
    <img src="https://github.com/charmbracelet/mods/assets/25087/5442bf46-b908-47af-bf4e-60f7c38951c4" width="630" alt="Mods product art and type treatment"/>
    <br>
    <a href="https://github.com/panjie/mods/releases"><img src="https://img.shields.io/github/release/panjie/mods.svg" alt="Latest Release"></a>
    <a href="https://github.com/panjie/mods/actions"><img src="https://github.com/panjie/mods/workflows/build/badge.svg" alt="Build Status"></a>
</p>

AI for the command line, built for pipelines.

<p><img src="https://vhs.charm.sh/vhs-5Uyj0U6Hlqi1LVIIRyYKM5.gif" width="900" alt="a GIF of mods running"></p>

Large Language Models (LLM) based AI is useful to ingest command output and
format results in Markdown, JSON, and other text based formats. Mods is a
tool to add a sprinkle of AI in your command line and make your pipelines
artificially intelligent.

It works great with LLMs running locally through [LocalAI]. You can also use
[OpenAI], [Cohere], [Groq], or [Azure OpenAI].

[LocalAI]: https://github.com/go-skynet/LocalAI
[OpenAI]: https://platform.openai.com/account/api-keys
[Cohere]: https://dashboard.cohere.com/api-keys
[Groq]: https://console.groq.com/keys
[Azure OpenAI]: https://azure.microsoft.com/en-us/products/cognitive-services/openai-service

### Installation

#### Build from source

```sh
git clone https://github.com/panjie/mods.git
cd mods
make build
```

This writes the binary to `bin/mods`. Add it to your `PATH` for easy access:

```sh
export PATH="$PWD/bin:$PATH"
```

Use `make check` to verify the project compiles, and `make test` to run the test suite. The `dist/` directory is reserved for GoReleaser release and snapshot artifacts.

#### Install with `go`

```sh
go install github.com/panjie/mods@latest
```

#### Download binaries

Pre-built [packages][releases] (Debian, RPM) and [binaries][releases] (Linux, macOS, Windows) are also available.

[releases]: https://github.com/panjie/mods/releases

<details>
<summary>Shell Completions</summary>

All the packages and archives come with pre-generated completion files for Bash,
ZSH, Fish, and PowerShell.

If you built it from source, you can generate them with:

```bash
mods completion bash -h
mods completion zsh -h
mods completion fish -h
mods completion powershell -h
```

If you installed via a binary package, the completions should be set
up automatically, given your shell is configured properly.

</details>

## What's New

This fork adds the following features on top of the original Mods.

### Tools & Agents

- **Web Search** — `--web-search` enables real-time web search (DuckDuckGo default,
  no API key required). Also supports Tavily and custom providers.
- **Image Recognition** — `-i` / `--image` attaches images for vision-capable
  models (GPT-4o, Claude, Gemini). `--clipboard-image` and `--stdin-image` for
  clipboard and piped image input.
- **Built-in Tools** — File system operations (`fs_read_file`, `fs_write_file`,
  `fs_search`, `fs_apply_patch`), shell execution (`shell_run`), and sequential
  thinking (`thinking_note`). Filesystem tools auto-activate when your prompt
  mentions files.

### Review & Safety

- **Tool Execution Review** — Before modifying files or running shell commands,
  Mods shows a colored confirmation banner. Press `Y` to approve once, `N` to
  deny, or `A` to save the displayed rule for the current conversation. Shell
  rules use command prefixes; file-edit rules cover future edits in that
  conversation. Saved rules are restored with `--continue`. `--review` controls the mode:
  `mutable` (default), `always`, or `never`.

  ```
  Review: Run: git commit -m "Update docs"
  [Y] Approve  [N] Deny  [A] Always allow: shell_run(git commit *)  [Ctrl+C] Cancel
  ```

### Observability

- **Debug Mode** — `--debug` / `-D` prints execution steps, tool calls, reasoning
  thoughts, and API diagnostics to stderr.
- **Status Line** — Live status bar shows what the model is doing: "Reading file:
  ...", "Running command: ...", "Searching web: ...".
- **Tool Call Display** — `--show-tool-calls` controls whether tool call details
  appear in the output.

### Quality of Life

- **Reasoning Mode** — `--reasoning on|off|auto`: auto mode judges task complexity
  before engaging deep reasoning (saves tokens for simple queries).
- **Minimal Pipeline Output** — `--minimal` tells the model to skip explanations
  and output one item per line, optimized for `|` pipelines.
- **Tool Round Limits** — `--max-tool-rounds` caps total tool call rounds (default
  30) with separate failed-round limiting to prevent infinite loops.
- **Updated Models** — Model list refreshed to latest releases across OpenAI,
  Anthropic, Google, Cohere, and DeepSeek.

### Cross-Platform

- **Windows** — Console popup suppression, clipboard support, platform-specific
  command execution.
- **Stability** — 413/token overflow prevention, MCP timeout handling, session
  error recovery.

## What Can It Do?

Mods works by reading standard in and prefacing it with a prompt supplied in
the `mods` arguments. It sends the input text to an LLM and prints out the
result, optionally asking the LLM to format the response as Markdown. This
gives you a way to "question" the output of a command. Mods will also work on
standard in or an argument supplied prompt individually.

Be sure to check out the [examples](examples.md) and a list of all the
[features](features.md).

Mods works with OpenAI compatible endpoints. By default, Mods is configured to
support OpenAI's official API and a LocalAI installation running on port 8080.
You can configure additional endpoints in your settings file by running
`mods --settings`.

## Saved Conversations

Conversations are saved locally by default. Each conversation has a SHA-1
identifier and a title (like `git`!).

<p>
  <img src="https://vhs.charm.sh/vhs-6MMscpZwgzohYYMfTrHErF.gif" width="900" alt="a GIF listing and showing saved conversations.">
</p>

Check the [`./features.md`](./features.md) for more details.

## Pipeline-Friendly Output

Use `--minimal` when another command needs to consume the answer directly. It tells the model to skip explanations and print list results one item per line.

```sh
ls -l | mods --minimal "pick the biggest five file names" | gum choose
```

## Usage

- `-m`, `--model`: Specify Large Language Model to use
- `-M`, `--ask-model`: Ask which model to use via interactive prompt
- `-f`, `--format`: Ask the LLM to format the response in a given format
- `--format-as`: Specify the format for the output (used with `--format`)
- `--minimal`: Output only the final result, optimized for pipelines
- `-P`, `--prompt` Include the prompt from the arguments and stdin, truncate stdin to specified number of lines
- `-p`, `--prompt-args`: Include the prompt from the arguments in the response
- `-q`, `--quiet`: Only output errors to standard err
- `-r`, `--raw`: Print raw response without syntax highlighting
- `--settings`: Open settings
- `-x`, `--http-proxy`: Use HTTP proxy to connect to the API endpoints
- `--max-retries`: Maximum number of retries
- `--max-tokens`: Specify maximum tokens with which to respond
- `--no-limit`: Do not limit the response tokens
- `--role`: Specify the role to use (See [custom roles](#custom-roles))
- `--word-wrap`: Wrap output at width (defaults to 80)
- `--reset-settings`: Restore settings to default
- `--theme`: Theme to use in the forms; valid choices are: `charm`, `catppuccin`, `dracula`, and `base16`
- `--status-text`: Text to show while generating

#### Image Support

- `-i`, `--image`: Attach one or more images to the prompt (supports png, jpg, gif, webp). Can be specified multiple times or as comma-separated paths
- `--stdin-image`: Treat piped stdin input as raw image data instead of text
- `--clipboard-image`: Attach the current image in the system clipboard to the prompt

#### Web Search

- `--web-search`: Enable web search for up-to-date information (uses DuckDuckGo by default)
- `--web-search-provider`: Web search provider (`duckduckgo`, `tavily`, or `custom`)
- `--web-search-api-key`: API key for the web search provider (required for Tavily)

#### Conversations

- `-t`, `--title`: Set the title for the conversation.
- `-l`, `--list`: List saved conversations.
- `-c`, `--continue`: Continue from last response or specific title or SHA-1.
- `-C`, `--continue-last`: Continue the last conversation.
- `-s`, `--show`: Show saved conversation for the given title or SHA-1
- `-S`, `--show-last`: Show previous conversation
- `--delete-older-than=<duration>`: Deletes conversations older than given duration (`10d`, `1mo`).
- `--delete`: Deletes the saved conversations for the given titles or SHA-1s
- `--no-cache`: Do not save conversations

#### Review & Safety

- `-V`, `--review`: Set review mode: `mutable` (default, reviews file writes and shell commands), `always` (reviews all tools), or `never` (disables review). Also configurable via `MODS_REVIEW_MODE` env var or `review-mode` in `mods.yml`.
- `--max-tool-rounds`: Maximum total tool call rounds before stopping (default 30)

```bash
# Review all tool executions
mods --review always "rename the fn to calculateTotal"
# Disable review entirely
mods --review never "list go files"
```

#### Reasoning & Debug

- `-T`, `--reasoning`: Deep reasoning mode: `off`, `on`, or `auto` (judges task complexity before engaging, saves tokens on simple queries)
- `-D`, `--debug`: Print execution steps, tool calls, and request diagnostics to stderr

#### MCP

- `--mcp-list`: List all available MCP servers
- `--mcp-list-tools`: List all available tools from enabled MCP servers
- `--mcp-disable`: Disable specific MCP servers
- `--mcp-enable`: Enable only specific MCP servers (whitelist)

#### Advanced

- `--fanciness`: Level of fanciness
- `--temp`: Sampling temperature
- `--topp`: Top P value
- `--topk`: Top K value

## Custom Roles

Roles allow you to set system prompts. Here is an example of a `shell` role:

```yaml
roles:
  shell:
    - you are a shell expert
    - you do not explain anything
    - you simply output one liners to solve the problems you're asked
    - you do not provide any explanation whatsoever, ONLY the command
```

Then, use the custom role in `mods`:

```sh
mods --role shell list files in the current directory
```

## Setup

### Open AI

Mods uses GPT-4 by default. It will fall back to GPT-3.5 Turbo.

Set the `OPENAI_API_KEY` environment variable. If you don't have one yet, you
can grab it the [OpenAI website](https://platform.openai.com/account/api-keys).

Alternatively, set the [`AZURE_OPENAI_KEY`] environment variable to use Azure
OpenAI. Grab a key from [Azure](https://azure.microsoft.com/en-us/products/cognitive-services/openai-service).

### Cohere

Cohere provides enterprise optimized models.

Set the `COHERE_API_KEY` environment variable. If you don't have one yet, you can
get it from the [Cohere dashboard](https://dashboard.cohere.com/api-keys).

### Local AI

Local AI allows you to run models locally. Mods works with the GPT4ALL-J model
as setup in [this tutorial](https://github.com/go-skynet/LocalAI#example-use-gpt4all-j-model).

### Groq

Groq provides models powered by their LPU inference engine.

Set the `GROQ_API_KEY` environment variable. If you don't have one yet, you can
get it from the [Groq console](https://console.groq.com/keys).

### Gemini

Mods supports using Gemini models from Google.

Set the `GOOGLE_API_KEY` environment variable. If you don't have one yet,
you can get it from the [Google AI Studio](https://aistudio.google.com/apikey).

### Web Search

Mods can search the web to provide up-to-date information in responses. DuckDuckGo is the default provider and requires no API key.

```bash
# Enable web search
mods --web-search "What's the latest Go version?"
```

**Tavily** provides AI-optimized search results. Set your API key:

```bash
export MODS_WEB_SEARCH_PROVIDER=tavily
export MODS_WEB_SEARCH_API_KEY=tvly-xxxxxxxxxxxxx
```

**Custom providers** are supported via a base URL pointing to a compatible search API:

```bash
mods --web-search --web-search-provider=https://your-search-api.example.com "query"
```

## Contributing

Issues and pull requests are welcome on this fork.

## License

[MIT](https://github.com/panjie/mods/raw/main/LICENSE)
