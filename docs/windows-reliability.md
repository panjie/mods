# Windows real-machine reliability

The Windows reliability suite complements cross-compilation and ordinary unit
tests. It starts real PowerShell and child processes, exercises Job Objects and
Windows path resolution, and writes JSON/Markdown evidence under
`artifacts/windows-reliability`.

## Local runs

Run a short deterministic pass from Windows PowerShell 5.1:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts/windows-reliability.ps1 -Mode quick -PowerShellHost ps51
```

Run the stress pass (100 exit/timeout race iterations):

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts/windows-reliability.ps1 -Mode stress -PowerShellHost auto
```

Use `-PowerShellHost ps7` to require PowerShell 7. The PS5.1 lane removes the
directory containing `pwsh.exe` from the child PATH, ensuring the classifier
and executor both resolve `powershell.exe`.

Set `TEST_UNC_ROOT` to an accessible real UNC directory such as
`\\server\share\mods-test`. Without it, UNC validation is reported as
`blocked/not covered`; it is never silently counted as passing. Node/npm,
WinGet, and Scoop are handled the same way when unavailable.

## CI layers

- Pull requests run both PS5.1 and PS7 quick lanes on `windows-latest` after the
  normal CI baseline.
- `.github/workflows/windows-reliability.yml` runs the 100-iteration matrix on
  a schedule and on demand, and uploads raw JSONL plus JSON/Markdown reports.
- A self-hosted runner labelled `windows-reliability` is enabled by setting the
  repository variable `WINDOWS_RELIABILITY_SELF_HOSTED=true`. Configure
  `WINDOWS_RELIABILITY_UNC_ROOT` for UNC coverage and run that worker inside the
  desired terminal, IDE, enterprise token, or outer Job Object environment.

A report sets `production_ready` only when the test command succeeds and every
case passes. Any skipped environmental requirement leaves it false. Promotion
to “production-grade Windows reliability” therefore requires green reports
from x64 and ARM64 machines, PS5.1 and PS7, hosted CI, and the configured
self-hosted UNC/restricted-Job environment.
