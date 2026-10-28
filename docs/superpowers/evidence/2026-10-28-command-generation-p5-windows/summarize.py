#!/usr/bin/env python3
"""Summarize the P5 command-generation reports.

This is evidence tooling, not production code. It reads the saved raw JSON from
the A/A1 (pre-P2) and B/C (current) runs and writes summary.json plus the
Markdown tables used in README.md. Correctness and zero-review values stay
unavailable (null) when the report does not contain them.
"""
from __future__ import annotations

import argparse
import json
import statistics
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument("--lane", choices=["ps7", "ps51"], default="ps7")
args = parser.parse_args()
ROOT = Path(__file__).resolve().parent
if args.lane == "ps51":
    ROOT = ROOT / "ps51"


def load_runs(path: Path):
    data = json.loads(path.read_text(encoding="utf-8-sig"))
    if isinstance(data, list):
        return data
    return data["runs"]


def runs_for(group: str, mode: str, phase: str):
    if group == "A/A1":
        suffix = "true" if mode == "minimal" else "false"
        name = f"generation-{phase}-minimal-{suffix}.json"
    else:
        suffix = "true" if mode == "minimal" else "false"
        name = f"p5-{phase}-minimal-{suffix}.json"
    return load_runs(ROOT / "raw" / ("a1" if group == "A/A1" else "bc") / mode / name)


def n(value):
    return 0 if value is None else value


def count_bool(runs, key):
    values = [run.get(key) for run in runs]
    return sum(v is True for v in values), sum(v is False for v in values), sum(v is None for v in values)


def aggregate(runs):
    out = {}
    for arm in sorted({run["arm"] for run in runs}):
        selected = [run for run in runs if run["arm"] == arm]
        calls = [call for run in selected for call in (run.get("calls") or [])]
        outcomes = [run.get("outcome", {}) for run in selected]
        correct = count_bool([run.get("outcome", {}) for run in selected], "correct")
        zero = count_bool(selected, "zero_review_completion")
        p4 = "automatic_reorganizations" in (outcomes[0] if outcomes else {})
        tokens = sum(n(run.get("input_tokens")) + n(run.get("output_tokens")) for run in selected)
        elapsed = [run.get("elapsed_ms", 0) for run in selected]
        out[arm] = {
            "runs": len(selected),
            "trusted_dispatches": None,
            "execution_failures": None,
            "actual_review_prompts": sum(o["actual_review_prompts"] for o in outcomes) if all(o.get("actual_review_prompts") is not None for o in outcomes) else None,
            "errors": sum(bool(run.get("error")) for run in selected),
            "completed": sum(bool(o.get("completed")) for o in outcomes),
            "correct_true": correct[0],
            "correct_false": correct[1],
            "correct_null": correct[2],
            "zero_true": zero[0],
            "zero_false": zero[1],
            "zero_null": zero[2],
            "approval_unavailable": sum(n(o.get("approval_unavailable")) for o in outcomes),
            "automatic_reorganizations": sum(n(o.get("automatic_reorganizations")) for o in outcomes) if p4 else None,
            "manual_reorganizations": sum(n(o.get("manual_reorganizations")) for o in outcomes) if p4 else None,
            "tool_errors": sum(n(o.get("tool_errors")) for o in outcomes) if p4 else None,
            "allow_path_errors": sum(n(o.get("allow_path_errors")) for o in outcomes) if p4 else None,
            "tool_calls": sum(n(o.get("tool_calls")) for o in outcomes),
            "tool_rounds": sum(n(o.get("tool_rounds")) for o in outcomes),
            "tokens": tokens,
            "median_ms": int(statistics.median(elapsed)) if elapsed else 0,
            "max_ms": max(elapsed) if elapsed else 0,
            "calls": len(calls),
            "static_reads": sum(bool(call.get("static_read")) for call in calls),
            "predicted_ask": sum(call.get("predicted_decision") == "ask" for call in calls),
            "native_reads": sum(call.get("read_basis") == "native_read_contract" for call in calls),
            "opaque": sum(bool(call.get("opaque")) for call in calls),
            "writes": sum(call.get("effect") == "write" for call in calls),
            "organization_reasons": sum(bool(call.get("organization_reasons")) for call in calls) if p4 else None,
        }
    return out


def fmt(value):
    return "n/a" if value is None else str(value)


def main():
    summary = {"groups": {}}
    lines = []
    for group in ("A/A1", "B/C"):
        summary["groups"][group] = {}
        for mode in ("normal", "minimal"):
            summary["groups"][group][mode] = {}
            for phase in ("first-round", "completion"):
                runs = runs_for(group, mode, phase)
                cases = sorted({run["case"] for run in runs})
                per_case = {}
                for case in cases:
                    case_runs = [run for run in runs if run["case"] == case]
                    per_case[case] = aggregate(case_runs)
                summary["groups"][group][mode][phase] = {"arms": aggregate(runs), "cases": per_case}

    lines.append("# Generated P5 tables\n")
    lines.append("## First-round summary\n")
    lines.append("| Group | Mode | Arm | Runs | Calls | Static read | Predicted ask | Native read | Opaque | Write | Org reasons | Errors | Median ms |")
    lines.append("| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |")
    for group in ("A/A1", "B/C"):
        for mode in ("normal", "minimal"):
            for arm, a in summary["groups"][group][mode]["first-round"]["arms"].items():
                lines.append(f"| {group} | {mode} | {arm} | {a['runs']} | {a['calls']} | {a['static_reads']} | {a['predicted_ask']} | {a['native_reads']} | {a['opaque']} | {a['writes']} | {fmt(a['organization_reasons'])} | {a['errors']} | {a['median_ms']} |")
    lines.append("\n## Completion summary\n")
    lines.append("| Group | Mode | Arm | Runs | Completed | Correct | Incorrect | Unavailable | Zero-review | Approval unavailable | Auto reorg | Manual reorg | Tool errors | Allow-path errors | Calls | Rounds | Median ms |")
    lines.append("| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |")
    for group in ("A/A1", "B/C"):
        for mode in ("normal", "minimal"):
            for arm, a in summary["groups"][group][mode]["completion"]["arms"].items():
                lines.append(f"| {group} | {mode} | {arm} | {a['runs']} | {a['completed']} | {a['correct_true']} | {a['correct_false']} | {a['correct_null']} | {a['zero_true']}/{a['zero_false']}/{a['zero_null']} | {a['approval_unavailable']} | {fmt(a['automatic_reorganizations'])} | {fmt(a['manual_reorganizations'])} | {fmt(a['tool_errors'])} | {fmt(a['allow_path_errors'])} | {a['tool_calls']} | {a['tool_rounds']} | {a['median_ms']} |")
    lines.append("\n## Completion by case\n")
    lines.append("| Group | Mode | Arm | Case | Runs | Correct | Zero-review | Approval unavailable | Auto reorg | Calls | Rounds |")
    lines.append("| --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |")
    for group in ("A/A1", "B/C"):
        for mode in ("normal", "minimal"):
            for case, arms in summary["groups"][group][mode]["completion"]["cases"].items():
                for arm, a in arms.items():
                    lines.append(f"| {group} | {mode} | {arm} | {case} | {a['runs']} | {a['correct_true']}/{a['correct_false']}/{a['correct_null']} | {a['zero_true']}/{a['zero_false']}/{a['zero_null']} | {a['approval_unavailable']} | {fmt(a['automatic_reorganizations'])} | {a['tool_calls']} | {a['tool_rounds']} |")

    (ROOT / "summary.json").write_text(json.dumps(summary, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    (ROOT / "tables.md").write_text("\n".join(lines) + "\n", encoding="utf-8")
    print("\n".join(lines))


if __name__ == "__main__":
    main()
