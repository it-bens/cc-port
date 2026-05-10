# demo-export-import — the paired flow: export an archive on one machine,
# import it on another. Demonstrated with two distinct synthetic HOMEs and
# a PS1 swap to signal the second machine.

Output docs/images/demo-export-import.gif
Output docs/images/demo-export-import.mp4

Set Width 1200
Set Height 700
Set FontSize 22
Set TypingSpeed 60ms
Set PlaybackSpeed 1.0
Set Padding 20

# Source-machine bootstrap.
Hide
Type 'export SOURCE_HOME="$(mktemp -d /tmp/ccp.XXXXXX)"' Enter
Type 'export TARGET_HOME="$(mktemp -d /tmp/ccp.XXXXXX)"' Enter
Type 'mkdir -p /tmp/cc-port-demo-share' Enter
Type 'export HOME="$SOURCE_HOME"' Enter
Type 'export PS1="me@laptop:~\$ "' Enter
Type 'clear' Enter
Type 'bash docs/videos/fixtures/seed-source.sh /Users/demo/source-project >/dev/null' Enter
Type 'clear' Enter
Show

# Source machine: export.
Type 'cc-port export /Users/demo/source-project --output /tmp/cc-port-demo-share/project.zip --all'
Sleep 500ms
Enter
Sleep 4s

# Swap to teammate HOME and prompt.
Hide
Type 'export HOME="$TARGET_HOME"' Enter
Type 'export PS1="teammate@laptop:~\$ "' Enter
Type 'bash docs/videos/fixtures/seed-target.sh >/dev/null' Enter
Type 'clear' Enter
Show

# Teammate machine: import.
Type 'cc-port import /tmp/cc-port-demo-share/project.zip /Users/demo/teammate-project'
Sleep 500ms
Enter
Sleep 4s

Type 'ls ~/.claude/projects/'
Sleep 500ms
Enter
Sleep 3s
