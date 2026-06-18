# Mods Examples

### Mods At A Glance

The README demo shows Mods handling common command-line AI workflows in one
short session: summarizing piped input, searching the web, describing an image,
and producing minimal output for another pipeline step.

`printf '%s\n' '[{"name":"bubbletea"},{"name":"lipgloss"},{"name":"gum"},{"name":"vhs"}]' | mods -f "summarize these repositories"`

`mods --web-search "What changed in the latest Go release?"`

`mods -i examples/gifs/mods-product.png "Describe this image and suggest README alt text"`

`find . -maxdepth 1 -type f | sed 's#^./##' | sort | mods --minimal "pick the five files most relevant to built-in tools"`

<p><img src="examples/gifs/demo.gif" width="900" alt="a GIF of mods demonstrating pipelines, web search, image input, and minimal output"></p>

### Choose A Model

Use `--ask-model` (`-M`) to select a configured model interactively before
running a prompt.

`mods -M "Hello world"`

<p><img src="examples/gifs/mods-v1.5.gif" width="900" alt="a GIF of mods selecting a model"></p>

### Continue Saved Conversations

List local conversations and show a previous response by ID.

`mods --list`

`mods --show 8a0d428`

<p><img src="examples/gifs/conversations.gif" width="900" alt="a GIF listing and showing saved conversations"></p>

### Pipeline-Friendly Output

Use `--minimal` when another command needs to consume the answer directly.

`find . -maxdepth 1 -type f | sed 's#^./##' | sort | mods --minimal "pick the five files most relevant to built-in tools"`

<p><img src="examples/gifs/minimal-pipeline.gif" width="900" alt="a GIF of mods returning minimal pipeline output"></p>

### Search The Web

Enable web search when the answer needs current information.

`mods --web-search "What changed in the latest Go release?"`

<p><img src="examples/gifs/web-search.gif" width="900" alt="a GIF of mods searching the web"></p>

### Understand Images

Attach images to prompts with `-i` or `--image`.

`mods -i examples/gifs/mods-product.png "Describe this image and suggest README alt text"`

<p><img src="examples/gifs/image-vision.gif" width="900" alt="a GIF of mods describing an image"></p>

### Review Tool Execution

Built-in tools can read, write, search, patch, and run shell commands. Review
mode asks before mutable tool execution.

`mods --review mutable --workspace . "Read README.md and write docs/cli-notes.md with a short usage guide"`

<p><img src="examples/gifs/builtin-tools-review.gif" width="900" alt="a GIF of mods asking for tool execution review"></p>

### Debug Reasoning

Use reasoning and debug output to inspect how Mods approaches a task.

`mods --reasoning auto --debug "When should I use each review mode?"`

<p><img src="examples/gifs/reasoning-debug.gif" width="900" alt="a GIF of mods showing reasoning and debug output"></p>
