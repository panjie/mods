"""Extract shell-only sequences from typed C events for reviewed mock replay.

Review every generated expectation before running/committing. This never
updates policy manifests or any historical safety baseline.
"""
import json
from pathlib import Path

root = Path(__file__).resolve().parent
repo = root.parents[3]
fixtures = []
for path in sorted(root.rglob("p5-completion-*.json")):
    host = "ps51" if "ps51" in path.relative_to(root).parts else "ps7"
    for run in json.loads(path.read_text(encoding="utf-8"))["runs"]:
        if not run["outcome"]["automatic_reorganizations"]:
            continue
        assert run["arm"] == "C"
        assert run["outcome"]["automatic_reorganizations"] == 1
        used = False
        calls = []
        for call in run["calls"]:
            if call["name"] not in ("shell_run", "powershell_run"):
                continue
            want = call["predicted_decision"]
            if call.get("organization_reasons") and not used:
                want = "reorganize"
                used = True
            calls.append(dict(name=call["name"], args=call["args"], want=want))
        assert used
        mode = "minimal" if run["minimal"] else "normal"
        fixtures.append(dict(id=f"{host}-{mode}-{run['case']}-{run['repeat']}",
                             source_report=path.relative_to(root).as_posix(),
                             case=run["case"], minimal=run["minimal"], calls=calls))
target = repo / "internal/app/testdata/command_generation/p5_windows_reorganizations.json"
target.write_text(json.dumps(fixtures, ensure_ascii=False, indent=2) + "\n", encoding="utf-8", newline="\n")
print(f"Extracted {len(fixtures)} replay fixtures; review expectations before testing")
