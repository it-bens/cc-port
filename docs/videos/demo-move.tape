# demo-move — the rename narrative. Also serves as the README hero clip.
#
# Story: a project folder was renamed from /Users/demo/old-project to
# /Users/demo/new-project. cc-port rewrites the Claude Code state to match.

Output docs/images/demo-move.gif
Output docs/images/demo-move.mp4

Set Width 1650
Set Height 700
Set FontSize 22
Set TypingSpeed 60ms
Set PlaybackSpeed 1.0
Set Padding 20

# Bootstrap: synthetic HOME with seeded ~/.claude/ for /Users/demo/old-project.
Hide
Type 'export HOME="$(mktemp -d /tmp/ccp.XXXXXX)"' Enter
Type 'export PS1="\$ "' Enter
Type 'clear' Enter
Type 'bash docs/videos/fixtures/seed-source.sh /Users/demo/old-project >/dev/null' Enter
Type 'clear' Enter
Show

# Demo sequence
Type 'cc-port move /Users/demo/old-project /Users/demo/new-project --refs-only'
Sleep 500ms
Enter
Sleep 3s

Type 'cc-port move /Users/demo/old-project /Users/demo/new-project --refs-only --apply'
Sleep 500ms
Enter
Sleep 3s

Type 'ls ~/.claude/projects/'
Sleep 500ms
Enter
Sleep 3s
