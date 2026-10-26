$exts = @('.go','.ts','.tsx','.js','.jsx','.py','.rs','.java','.c','.cpp','.h','.cs','.rb','.sh','.ps1','.sql','.yml','.yaml','.json','.md','.html','.css')
Get-ChildItem -Recurse -File | Where-Object { $exts -contains $_.Extension.ToLower() -and $_.FullName -notmatch '\\\.git\\' } |
  Group-Object { $_.Extension.ToLower() } |
  ForEach-Object {
    $lines = 0
    $files = 0
    foreach ($f in $_.Group) {
      $files++
      $c = (Get-Content -LiteralPath $f.FullName -ErrorAction SilentlyContinue | Measure-Object -Line).Lines
      if ($c) { $lines += $c }
    }
    [pscustomobject]@{ Ext = $_.Name; Files = $files; Lines = $lines }
  } | Sort-Object Lines -Descending | Format-Table -AutoSize
