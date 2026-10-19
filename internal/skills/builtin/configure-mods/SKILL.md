---
name: configure-mods
description: Edit the mods configuration safely. Use when the user asks to change a mods setting, provider, prompt, or model, and the request needs the active config file edited.
---

# Edit mods configuration

Use this when the user asks mods to change its own non-secret configuration.

1. Ask `mods_help` for the `config` topic (or use the supplied reference) to get
   the active config path and filesystem mode. Ask for missing context before
   editing.
2. With file tools, use `fs_search`, narrow `fs_read_file` ranges, and
   `fs_replace`. Preserve comments and unrelated YAML.
3. Never read, echo, or write credentials through ordinary file tools. Prefer
   `api-key-env`.
4. Changes take effect on the next mods invocation; tell the user.

Without filesystem tools, explain that direct editing is unavailable and give a
supported manual command or config change.
