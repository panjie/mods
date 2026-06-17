Output examples/gifs/mods.gif

Set Height 900
Set Width 1600
Set Framerate 24
Set FontSize 26

Hide
  Type `clear`
  Enter
  Sleep 300ms
Show

Sleep 500ms

Type@25ms `printf '%s\n' '[{"name":"bubbletea"},{"name":"lipgloss"},{"name":"gum"},{"name":"vhs"}]' | mods -f "summarize these repositories"`
Sleep 300ms
Enter
Sleep 2800ms

Ctrl+L
Sleep 500ms

Type@25ms `mods --web-search "What changed in the latest Go release?"`
Sleep 300ms
Enter
Sleep 2800ms

Ctrl+L
Sleep 500ms

Type@25ms `mods -i examples/gifs/mods-product.png "Describe this image and suggest README alt text"`
Sleep 300ms
Enter
Sleep 2800ms

Ctrl+L
Sleep 500ms

Type@25ms `find . -maxdepth 1 -type f | sed 's#^./##' | sort | mods --minimal "pick the five files most relevant to built-in tools"`
Sleep 300ms
Enter
Sleep 2800ms
