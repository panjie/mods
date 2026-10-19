---
name: code-metrics
description: Code size and line-count reports. Use when the user asks 代码量, 行数, LOC, how many lines a project has, or a physical/nonblank/comment/code-line breakdown.
---

# Code size and line-count reports

Use this when the user asks for code size, line counts, LOC, or a breakdown of
physical, blank, comment, or code lines. This is a reporting task: measure with
the fewest calls that answer the question.

1. State scope first: tracked vs untracked files, directories included or
   excluded, and whether generated or vendored code counts.
2. A plain "how many lines" question is one command:
   `git ls-files -z | xargs -0 wc -l | tail -1`. Report that total and stop.
3. Add physical/nonblank/comment/code distinctions only when the user asked for
   them or the report is explicitly about code metrics. A language-aware counter
   is required for comment exclusion (for Go: `go/scanner` or `go/parser`); a
   regex is wrong when `//` or `#` appears inside a string. When no parser exists
   for a language, report physical and nonblank counts and say the
   comment-excluded figure is unavailable instead of estimating it.
4. Label every figure with its counting rule, and never mix conventions in one
   total.
5. Do the measurement in a temporary directory and leave the project unchanged.

Cross-check the headline total once with an independent command. If the numbers
disagree, explain the difference instead of choosing one silently.
