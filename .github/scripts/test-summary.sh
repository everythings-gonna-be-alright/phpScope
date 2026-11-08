#!/usr/bin/env bash
# Renders a `go test -json` stream as a Markdown summary.
# Usage: test-summary.sh <title> <go-test-json-file>
set -euo pipefail

title=$1
json=$2

if [ ! -s "$json" ]; then
  printf '### %s\n\nNo test results were produced.\n' "$title"
  exit 0
fi

jq -rs --arg title "$title" '
  [.[] | select(.Test != null and (.Action == "pass" or .Action == "fail" or .Action == "skip"))] as $results
  | [$results[] | select(.Action == "fail") | .Test] as $failed
  | [.[] | select(.Action == "output" and .Test != null and (.Output | test("^\\s+\\S+_test\\.go:[0-9]+: ")))
         | .Output | sub("^\\s+"; "") | rtrimstr("\n")] as $logs
  | "### \($title)\n",
    "| Passed | Failed | Skipped |",
    "|---|---|---|",
    "| \([$results[] | select(.Action == "pass")] | length) | \($failed | length) | \([$results[] | select(.Action == "skip")] | length) |",
    (if ($failed | length) > 0 then "\n**Failed:**\n", ($failed[] | "- `\(.)`") else empty end),
    (if ($logs | length) > 0 then "\n<details><summary>Test log</summary>\n\n```", $logs[], "```\n</details>" else empty end)
' "$json"
