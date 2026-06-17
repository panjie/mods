@echo off
setlocal enabledelayedexpansion

set "ARGS=%*"

if not "%ARGS:--web-search=%"=="%ARGS%" goto web_search
if not "%ARGS:--review=%"=="%ARGS%" goto tools_review
if not "%ARGS:--reasoning=%"=="%ARGS%" goto reasoning_debug
if not "%ARGS:--debug=%"=="%ARGS%" goto reasoning_debug
if not "%ARGS:--minimal=%"=="%ARGS%" goto minimal
if not "%ARGS:--clipboard-image=%"=="%ARGS%" goto vision
if not "%ARGS:--image=%"=="%ARGS%" goto vision
if not "%ARGS:-i =%"=="%ARGS%" goto vision

echo Mods demo response
exit /b 0

:web_search
echo Searching web: latest Go release changes
call :wait
echo.
echo ## Current answer
echo.
echo - Found recent release notes and compatibility updates.
echo - Summarized the changes instead of relying on stale training data.
echo - Included source links in the final answer when available.
exit /b 0

:vision
echo Reading image: examples/assets/build-error.png
call :wait
echo.
echo The screenshot shows a Go test failure in the cache package.
echo.
echo Suggested next steps:
echo 1. Check the expected cache TTL in cache_test.go.
echo 2. Re-run only the failing test with go test ./internal/cache -run TestExpiry.
echo 3. Inspect recent changes around expiration rounding.
exit /b 0

:tools_review
echo Reading file: README.md
call :wait
echo Writing file: docs/cli-notes.md
echo.
echo Review: Write docs/cli-notes.md (642 bytes)
echo [Y] Approve  [N] Deny  [A] Always allow: fs_write_file  [Ctrl+C] Cancel
set /p "answer="
if /I "%answer%"=="N" (
  echo Execution denied.
  exit /b 1
)
echo.
echo Wrote docs/cli-notes.md
echo Added a concise command-line usage section based on README.md.
exit /b 0

:minimal
call :wait
echo README.md
echo config.go
echo examples.go
echo internal/tools/builtin.go
echo review.go
exit /b 0

:reasoning_debug
echo [debug] API request - model=gpt-5 api=openai
echo [debug] Tools: 3 total tools
echo Deep reasoning...
call :wait
echo Thinking: compare risk, test coverage, and user-visible behavior
echo Searching files: ReviewMode in .
echo.
echo Recommendation: use --review mutable for normal coding, --review always for audits, and --review never only in trusted automation.
exit /b 0

:wait
ping -n 2 127.0.0.1 >nul
exit /b 0
