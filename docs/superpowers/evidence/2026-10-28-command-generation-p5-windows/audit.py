"""Validate complete sample cells and index exceptions without changing scores."""
import hashlib
import json
from pathlib import Path
from collections import Counter

ROOT = Path(__file__).resolve().parent
inventory = []
exceptions = []
for host, lane in (("ps7", ROOT), ("ps51", ROOT / "ps51")):
    for group in ("bc", "a1"):
        for mode in ("normal", "minimal"):
            for phase in ("first-round", "completion"):
                prefix = "p5" if group == "bc" else "generation"
                path = lane / "raw" / group / mode / f"{prefix}-{phase}-minimal-{str(mode == 'minimal').lower()}.json"
                data = json.loads(path.read_text(encoding="utf-8-sig"))
                runs = data["runs"] if isinstance(data, dict) else data
                want_arms = {"B", "C"} if group == "bc" else {"A", "A1"}
                expected_runs = 140 if phase == "first-round" else 40
                assert len(runs) == expected_runs, (path, len(runs))
                counts = Counter((r["case"], r["arm"]) for r in runs)
                assert all(n == 5 for n in counts.values()), (path, counts)
                for case, arm in counts:
                    assert {r["repeat"] for r in runs if r["case"] == case and r["arm"] == arm} == {1, 2, 3, 4, 5}
                assert {r["arm"] for r in runs} == want_arms
                assert len({(r["case"], r["arm"], r["repeat"]) for r in runs}) == len(runs)
                assert all(r["model"] == "deepseek-v4-flash" and r["os"] == "windows" and
                           r["temperature"] == 0 and r["max_tokens"] == 4096 and not r["think"] and
                           r["minimal"] == (mode == "minimal") and r["first_round_only"] == (phase == "first-round") for r in runs)
                assert len({r["policy_fingerprint"] for r in runs}) == 1
                if group == "bc":
                    assert all(r["review_mode"] == "auto" and r["command_simplification"] == ("off" if r["arm"] == "B" else "once") for r in runs)
                else:
                    assert all(r["baseline_revision"] == "original" for r in runs)
                for r in runs:
                    calls = r.get("calls") or []
                    reasons = [c for c in calls if c.get("organization_reasons")]
                    o = r["outcome"]
                    assert o.get("unexpected_writes", 0) == 0, (path, r["case"], r["arm"], r["repeat"])
                    labels = []
                    if reasons:
                        labels.append("candidate_structure")
                    if r.get("error"):
                        labels.append("run_error")
                    if phase == "completion" and o.get("correct") is False:
                        labels.append("incorrect")
                    if o.get("automatic_reorganizations"):
                        labels.append("typed_automatic_reorganization")
                    if o.get("approval_unavailable"):
                        labels.append("approval_unavailable")
                    if o.get("allow_path_errors"):
                        labels.append("allow_path_error_not_dispatch_proof")
                    if labels:
                        exceptions.append({"host": host, "file": path.relative_to(ROOT).as_posix(),
                                           "case": r["case"], "arm": r["arm"], "repeat": r["repeat"],
                                           "labels": labels, "error": r.get("error"), "answer": r.get("answer"),
                                           "outcome": o, "candidate_calls": reasons})
                inventory.append({"file": path.relative_to(ROOT).as_posix(), "host": host,
                                  "runs": len(runs), "sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
                                  "policy_fingerprint": runs[0]["policy_fingerprint"]})

assert sum(item["runs"] for item in inventory) == 1440
additional = []
for host in ("ps7", "ps51"):
    path = ROOT / "attempts/invalid-a1-selector" / host / "generation-first-round-minimal-false.json"
    runs = json.loads(path.read_text(encoding="utf-8-sig"))
    assert len(runs) == 70 and all(r["arm"] == "A1" for r in runs)
    additional.append({"file": path.relative_to(ROOT).as_posix(), "host": host, "runs": len(runs),
                       "excluded_from_primary": "Invalid prompt baseline selector prevented all A requests; unpaired A1 reports retained",
                       "sha256": hashlib.sha256(path.read_bytes()).hexdigest()})
(ROOT / "raw-inventory.json").write_text(json.dumps(inventory, indent=2) + "\n", encoding="utf-8")
(ROOT / "additional-runs.json").write_text(json.dumps(additional, indent=2) + "\n", encoding="utf-8")
(ROOT / "exceptions.json").write_text(json.dumps(exceptions, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
print(f"Validated {len(inventory)} primary reports / 1440 runs plus 140 retained unpaired runs; indexed {len(exceptions)} primary exceptional observations")
