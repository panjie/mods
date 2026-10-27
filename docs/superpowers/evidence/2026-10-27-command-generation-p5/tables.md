# Generated P5 tables

## First-round summary

| Group | Mode | Arm | Runs | Calls | Static read | Predicted ask | Native read | Opaque | Write | Org reasons | Errors | Median ms |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| A/A1 | normal | A | 70 | 90 | 44 | 15 | 31 | 14 | 5 | n/a | 0 | 987 |
| A/A1 | normal | A1 | 70 | 93 | 47 | 15 | 31 | 14 | 5 | n/a | 0 | 1072 |
| A/A1 | minimal | A | 70 | 86 | 34 | 15 | 37 | 10 | 5 | n/a | 0 | 879 |
| A/A1 | minimal | A1 | 70 | 89 | 41 | 15 | 33 | 10 | 5 | n/a | 0 | 925 |
| B/C | normal | B | 70 | 95 | 48 | 16 | 31 | 15 | 5 | 0 | 0 | 988 |
| B/C | normal | C | 70 | 90 | 45 | 15 | 30 | 15 | 5 | 0 | 0 | 995 |
| B/C | minimal | B | 70 | 89 | 42 | 17 | 30 | 10 | 5 | 0 | 0 | 983 |
| B/C | minimal | C | 70 | 87 | 44 | 16 | 27 | 10 | 5 | 0 | 0 | 972 |

## Completion summary

| Group | Mode | Arm | Runs | Completed | Correct | Incorrect | Unavailable | Zero-review | Approval unavailable | Auto reorg | Manual reorg | Tool errors | Allow-path errors | Calls | Rounds | Median ms |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| A/A1 | normal | A | 20 | 20 | 10 | 5 | 5 | 10/5/5 | 0 | n/a | n/a | n/a | n/a | 30 | 27 | 1834 |
| A/A1 | normal | A1 | 20 | 20 | 10 | 5 | 5 | 10/5/5 | 1 | n/a | n/a | n/a | n/a | 35 | 32 | 1843 |
| A/A1 | minimal | A | 20 | 20 | 10 | 5 | 5 | 10/5/5 | 0 | n/a | n/a | n/a | n/a | 30 | 30 | 1763 |
| A/A1 | minimal | A1 | 20 | 20 | 10 | 5 | 5 | 10/5/5 | 0 | n/a | n/a | n/a | n/a | 39 | 36 | 2010 |
| B/C | normal | B | 20 | 20 | 10 | 5 | 5 | 10/5/5 | 0 | 0 | 0 | 0 | 0 | 26 | 26 | 1941 |
| B/C | normal | C | 20 | 19 | 10 | 5 | 5 | 10/5/5 | 3 | 0 | 0 | 0 | 0 | 42 | 35 | 2042 |
| B/C | minimal | B | 20 | 20 | 10 | 5 | 5 | 10/5/5 | 0 | 0 | 0 | 0 | 0 | 30 | 30 | 1976 |
| B/C | minimal | C | 20 | 20 | 10 | 5 | 5 | 10/5/5 | 0 | 0 | 0 | 0 | 0 | 30 | 30 | 1797 |

## Completion by case

| Group | Mode | Arm | Case | Runs | Correct | Zero-review | Approval unavailable | Auto reorg | Calls | Rounds |
| --- | --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| A/A1 | normal | A | ambiguous-code | 5 | 0/0/5 | 0/0/5 | 0 | n/a | 15 | 12 |
| A/A1 | normal | A1 | ambiguous-code | 5 | 0/0/5 | 0/0/5 | 0 | n/a | 13 | 12 |
| A/A1 | normal | A | csv-sum | 5 | 5/0/0 | 5/0/0 | 0 | n/a | 5 | 5 |
| A/A1 | normal | A1 | csv-sum | 5 | 5/0/0 | 5/0/0 | 0 | n/a | 6 | 6 |
| A/A1 | normal | A | log-count | 5 | 5/0/0 | 5/0/0 | 0 | n/a | 5 | 5 |
| A/A1 | normal | A1 | log-count | 5 | 5/0/0 | 5/0/0 | 0 | n/a | 5 | 5 |
| A/A1 | normal | A | physical-lines | 5 | 0/5/0 | 0/5/0 | 0 | n/a | 5 | 5 |
| A/A1 | normal | A1 | physical-lines | 5 | 0/5/0 | 0/5/0 | 1 | n/a | 11 | 9 |
| A/A1 | minimal | A | ambiguous-code | 5 | 0/0/5 | 0/0/5 | 0 | n/a | 14 | 14 |
| A/A1 | minimal | A1 | ambiguous-code | 5 | 0/0/5 | 0/0/5 | 0 | n/a | 19 | 17 |
| A/A1 | minimal | A | csv-sum | 5 | 5/0/0 | 5/0/0 | 0 | n/a | 5 | 5 |
| A/A1 | minimal | A1 | csv-sum | 5 | 5/0/0 | 5/0/0 | 0 | n/a | 8 | 8 |
| A/A1 | minimal | A | log-count | 5 | 5/0/0 | 5/0/0 | 0 | n/a | 6 | 6 |
| A/A1 | minimal | A1 | log-count | 5 | 5/0/0 | 5/0/0 | 0 | n/a | 5 | 5 |
| A/A1 | minimal | A | physical-lines | 5 | 0/5/0 | 0/5/0 | 0 | n/a | 5 | 5 |
| A/A1 | minimal | A1 | physical-lines | 5 | 0/5/0 | 0/5/0 | 0 | n/a | 7 | 6 |
| B/C | normal | B | ambiguous-code | 5 | 0/0/5 | 0/0/5 | 0 | 0 | 10 | 10 |
| B/C | normal | C | ambiguous-code | 5 | 0/0/5 | 0/0/5 | 0 | 0 | 15 | 13 |
| B/C | normal | B | csv-sum | 5 | 5/0/0 | 5/0/0 | 0 | 0 | 6 | 6 |
| B/C | normal | C | csv-sum | 5 | 5/0/0 | 5/0/0 | 0 | 0 | 6 | 6 |
| B/C | normal | B | log-count | 5 | 5/0/0 | 5/0/0 | 0 | 0 | 5 | 5 |
| B/C | normal | C | log-count | 5 | 5/0/0 | 5/0/0 | 0 | 0 | 5 | 5 |
| B/C | normal | B | physical-lines | 5 | 0/5/0 | 0/5/0 | 0 | 0 | 5 | 5 |
| B/C | normal | C | physical-lines | 5 | 0/5/0 | 0/5/0 | 3 | 0 | 16 | 11 |
| B/C | minimal | B | ambiguous-code | 5 | 0/0/5 | 0/0/5 | 0 | 0 | 15 | 15 |
| B/C | minimal | C | ambiguous-code | 5 | 0/0/5 | 0/0/5 | 0 | 0 | 15 | 15 |
| B/C | minimal | B | csv-sum | 5 | 5/0/0 | 5/0/0 | 0 | 0 | 5 | 5 |
| B/C | minimal | C | csv-sum | 5 | 5/0/0 | 5/0/0 | 0 | 0 | 5 | 5 |
| B/C | minimal | B | log-count | 5 | 5/0/0 | 5/0/0 | 0 | 0 | 5 | 5 |
| B/C | minimal | C | log-count | 5 | 5/0/0 | 5/0/0 | 0 | 0 | 5 | 5 |
| B/C | minimal | B | physical-lines | 5 | 0/5/0 | 0/5/0 | 0 | 0 | 5 | 5 |
| B/C | minimal | C | physical-lines | 5 | 0/5/0 | 0/5/0 | 0 | 0 | 5 | 5 |
