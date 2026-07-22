# demo-move — the rename narrative. Also serves as the README hero clip.
#
# Story: a project folder was renamed from /Users/demo/old-project to
# /Users/demo/new-project. cc-port rewrites the Claude Code and Codex state to match.

Output docs/images/demo-move.gif
Output docs/images/demo-move.mp4

Set Width 1650
Set Height 1000
Set FontSize 22
Set TypingSpeed 20ms
Set PlaybackSpeed 1.0
Set Padding 20

# Bootstrap: synthetic HOME with seeded Claude Code and Codex state for /Users/demo/old-project.
Hide
Type 'export HOME="$(mktemp -d /tmp/ccp.XXXXXX)"' Enter
Type 'export PS1="\$ "' Enter
Type 'clear' Enter
Type 'seed-home --home "$HOME" --project /Users/demo/old-project --role source >/dev/null' Enter
Sleep 3s
Type 'clear' Enter
Sleep 200ms
Show

# Demo sequence
Type 'cc-port move /Users/demo/old-project /Users/demo/new-project'
Sleep 500ms
Enter
Sleep 3s

Type 'cc-port move /Users/demo/old-project /Users/demo/new-project --apply'
Sleep 500ms
Enter
Sleep 3s

Type 'cc-port stats /Users/demo/new-project'
Sleep 500ms
Enter
Sleep 3s
