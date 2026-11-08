#!/usr/bin/env bash
# Prints the CHANGELOG.md section for a release tag, e.g. release-notes.sh v0.1.4
set -euo pipefail

tag="${1:?usage: release-notes.sh <tag> [changelog]}"
changelog="${2:-CHANGELOG.md}"

notes="$(awk -v tag="$tag" '
  /^## \[/ {
    if (found) exit
    found = index($0, "[" tag "]") > 0
    next
  }
  found { print }
' "$changelog")"

if [[ -z "${notes//[[:space:]]/}" ]]; then
  echo "no CHANGELOG.md section found for ${tag}" >&2
  exit 1
fi

printf '%s\n' "$notes"
