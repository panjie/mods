"""Local fixed-response provider for HUMAN acceptance of the real mods UI.

No external model, credential, or user file is used. Run from a real terminal.
Each run copies bin/mods.exe into a fresh synthetic portable directory.
Server tool results are observations, never treated as trusted control events.
"""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

parser = argparse.ArgumentParser()
parser.add_argument("case", choices=["unknown", "write", "incomplete", "pages", "preview", "change"])
parser.add_argument("--host", choices=["ps51", "ps7"], default="ps7")
parser.add_argument("--mutate", type=Path, help="Synthetic run directory printed by an active preview/change run")
args = parser.parse_args()
if args.mutate:
    root = args.mutate.resolve(strict=True)
    if root.parent != Path(tempfile.gettempdir()).resolve() or not root.name.startswith("mods-p5-ui-"):
        raise SystemExit("Refusing a non-fixture directory")
    if (root / "fixture-marker.json").read_text(encoding="utf-8") != '{"synthetic":true}\n':
        raise SystemExit("Fixture marker mismatch")
    script = root / "synthetic-script.ps1"
    with script.open("a", encoding="utf-8", newline="\n") as f:
        f.write("Write-Output 'FIXED-SYNTHETIC-CHANGED'\n")
    print("Appended a fixed harmless output line; return to the pending review.")
    raise SystemExit(0)

repo = Path(__file__).resolve().parents[4]
binary = repo / "bin" / "mods.exe"
if not binary.is_file():
    raise SystemExit("Build bin/mods.exe first")
root = Path(tempfile.mkdtemp(prefix="mods-p5-ui-"))
(root / "fixture-marker.json").write_text('{"synthetic":true}\n', encoding="utf-8")
shutil.copy2(binary, root / "mods.exe")
host = shutil.which("powershell.exe" if args.host == "ps51" else "pwsh.exe")
if not host:
    raise SystemExit("Requested real host unavailable")
script = root / "synthetic-script.ps1"
script.write_text("# Fixed synthetic preview; no user data\n" + "\n".join(
    f"# Page fixture line {n}: synthetic content" for n in range(1, 65)
) + "\nWrite-Output 'FIXED-SYNTHETIC-EXECUTED'\n", encoding="utf-8")
long_dir = root / ("synthetic-long-path-" * 3 + "中文")
long_dir.mkdir()
target = (long_dir / "synthetic-output.txt").as_posix().replace("'", "''")
if args.case in ("preview", "change"):
    call = {"name": "process_run", "arguments": {"program": host, "args": ["-NoProfile", "-NonInteractive", "-File", str(script)]}}
else:
    commands = {
        "unknown": "Write-Output ([Math]::Abs(-7))",
        "write": f"Set-Content -LiteralPath '{target}' -Value 'SYNTHETIC-ONLY'",
        "incomplete": f"Set-Content -LiteralPath (Join-Path '{root.as_posix()}' 'synthetic-output.txt') -Value 'SYNTHETIC-ONLY'",
        "pages": "\n".join(f"Write-Output ([Math]::Abs(-{n})) # synthetic line {n}" for n in range(1, 81)),
    }
    call = {"name": "powershell_run", "arguments": {"command": commands[args.case]}}
(root / "fixed-call.json").write_text(json.dumps(call, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_POST(self):
        request = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        results = [m for m in request.get("messages", []) if m.get("role") == "tool"]
        if results:
            # This is an ordinary wire result, not a trusted dispatch/control metric.
            (root / "observed-tool-results.json").write_text(json.dumps(results, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
            delta = {"role": "assistant", "content": "Fixed provider received the tool result. Human acceptance must be recorded separately."}
            finish = "stop"
        else:
            delta = {"role": "assistant", "tool_calls": [{"index": 0, "id": "fixed-synthetic-call", "type": "function", "function": {"name": call["name"], "arguments": json.dumps(call["arguments"], ensure_ascii=False)}}]}
            finish = "tool_calls"
        chunk = {"id": "fixed-provider", "object": "chat.completion.chunk", "choices": [{"index": 0, "delta": delta, "finish_reason": finish}]}
        data = ("data: " + json.dumps(chunk, ensure_ascii=False) + "\n\ndata: [DONE]\n\n").encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
threading.Thread(target=server.serve_forever, daemon=True).start()
config = f"""default-api: fixture
default-model: fixed
review-mode: always
command-simplification: off
no-save: true
no-instructions: true
max-retries: 0
web-search: false
skills-dirs: [{json.dumps(str(root / 'absent-skills'))}]
builtin-tools:
  shell: true
  filesystem: always
apis:
  fixture:
    base-url: http://127.0.0.1:{server.server_port}/v1
    api-key: synthetic-not-a-secret
    models:
      fixed:
        endpoint: chat-completions
"""
(root / "mods.yml").write_text(config, encoding="utf-8")
env = os.environ.copy()
for name in list(env):
    if name.startswith("MODS_") or "API_KEY" in name:
        env.pop(name, None)
if args.host == "ps51":
    env["PATH"] = os.pathsep.join(p for p in env["PATH"].split(os.pathsep) if p and not (Path(p.strip('"')) / "pwsh.exe").exists())
print(f"Synthetic directory: {root}", flush=True)
print(f"Case: {args.case}; host: {host}; fixed call: {root / 'fixed-call.json'}", flush=True)
print("For change review, use another terminal to run this script with change --mutate and that directory.", flush=True)
try:
    code = subprocess.call([str(root / "mods.exe"), "--no-save", "--no-instructions", "Run the fixed synthetic UI acceptance case."], cwd=root, env=env)
except KeyboardInterrupt:
    code = 130
finally:
    server.shutdown()
print(f"CLI exit: {code}; human result NOT inferred. Synthetic files retained at {root}")
raise SystemExit(code)
