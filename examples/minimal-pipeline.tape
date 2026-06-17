Output examples/gifs/minimal-pipeline.gif

Set Height 700
Set Width 1400
Set Framerate 18
Set FontSize 26

Hide
  Type `clear`
  Enter
  Sleep 300ms
Show

Sleep 500ms
Type@25ms `find . -maxdepth 1 -type f | sed 's#^./##' | sort | mods --minimal "pick the five files most relevant to built-in tools"`
Sleep 400ms
Enter
Sleep 4s
