#!/usr/bin/env bash
# seed-source.sh — populate a synthetic $HOME/.claude/ for the demo's "source" HOME.
#
# Idempotent: re-running overwrites the same fixture content.
# Required env: HOME (a writable directory, typically from `mktemp -d` in the tape's Hide block).
# Required arg: $1 — the demo project path to encode (e.g. /Users/demo/source-project).
#
# All paths and content here are SYNTHETIC. No real maintainer data is ever
# included. File-history is seeded as an opaque empty directory (per the
# file-history opacity policy in docs/architecture.md).

set -euo pipefail

if [[ -z "${HOME:-}" ]]; then
	echo "seed-source.sh: HOME is unset" >&2
	exit 1
fi
if [[ ! -d "$HOME" ]]; then
	echo "seed-source.sh: HOME ($HOME) does not exist" >&2
	exit 1
fi
if [[ $# -ne 1 ]]; then
	echo "usage: seed-source.sh <demo-project-path>" >&2
	exit 1
fi

DEMO_PROJECT="$1"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

# Compute the encoded directory name via cc-port's own helper.
ENCODED="$(go run "$REPO_ROOT/docs/videos/fixtures/cmd/encode-path" "$DEMO_PROJECT")"

PROJECTS_DIR="$HOME/.claude/projects/$ENCODED"
SESSION_FILE="$PROJECTS_DIR/00000000-0000-0000-0000-000000000001.jsonl"

mkdir -p "$PROJECTS_DIR"
mkdir -p "$HOME/.claude/file-history"  # opaque per policy; empty is fine.

# Minimal session transcript: one user message and one assistant reply, both
# referencing the demo project path so a transcript-rewrite under
# --rewrite-transcripts has something to do.
cat > "$SESSION_FILE" <<JSONL
{"type":"user","cwd":"$DEMO_PROJECT","sessionId":"00000000-0000-0000-0000-000000000001","message":{"role":"user","content":[{"type":"text","text":"hello from $DEMO_PROJECT"}]},"timestamp":"2026-05-01T12:00:00Z"}
{"type":"assistant","cwd":"$DEMO_PROJECT","sessionId":"00000000-0000-0000-0000-000000000001","message":{"role":"assistant","content":[{"type":"text","text":"reply"}]},"timestamp":"2026-05-01T12:00:01Z"}
JSONL
chmod 600 "$SESSION_FILE"

# Minimal history.jsonl with one entry pointing at the demo project.
cat > "$HOME/.claude/history.jsonl" <<JSONL
{"project_path":"$DEMO_PROJECT","display":"$DEMO_PROJECT","pastedContents":{}}
JSONL
chmod 600 "$HOME/.claude/history.jsonl"

# Minimal ~/.claude.json referencing the demo project.
cat > "$HOME/.claude.json" <<JSON
{
  "numStartups": 1,
  "projects": {
    "$DEMO_PROJECT": {
      "allowedTools": [],
      "history": [],
      "mcpContextUris": []
    }
  }
}
JSON
chmod 600 "$HOME/.claude.json"

echo "Seeded source HOME at $HOME for demo project $DEMO_PROJECT (encoded: $ENCODED)" >&2
