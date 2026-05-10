#!/usr/bin/env bash
# seed-target.sh — produce a synthetic empty $HOME/.claude/ for the demo's "target" HOME.
#
# Idempotent. Required env: HOME (writable). Takes no arguments.
#
# The target HOME is what the teammate's machine looks like before they
# import or pull a project. It contains an empty .claude/projects/ and a
# minimal .claude.json with no projects.

set -euo pipefail

if [[ -z "${HOME:-}" ]]; then
	echo "seed-target.sh: HOME is unset" >&2
	exit 1
fi
if [[ ! -d "$HOME" ]]; then
	echo "seed-target.sh: HOME ($HOME) does not exist" >&2
	exit 1
fi

mkdir -p "$HOME/.claude/projects"
mkdir -p "$HOME/.claude/file-history"

cat > "$HOME/.claude.json" <<'JSON'
{
  "numStartups": 0,
  "projects": {}
}
JSON
chmod 600 "$HOME/.claude.json"

: > "$HOME/.claude/history.jsonl"
chmod 600 "$HOME/.claude/history.jsonl"

echo "Seeded empty target HOME at $HOME" >&2
