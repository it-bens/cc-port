# demo-push-pull — the paired remote flow. Source machine pushes; teammate
# pulls. Uses the project-stored Garage dev backend at http://localhost:9000.

Output docs/images/demo-push-pull.gif
Output docs/images/demo-push-pull.mp4

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
Type 'set -a; source dev/s3/env; set +a' Enter
Type 'export AWS_ACCESS_KEY_ID="$GARAGE_DEFAULT_ACCESS_KEY"' Enter
Type 'export AWS_SECRET_ACCESS_KEY="$GARAGE_DEFAULT_SECRET_KEY"' Enter
Type 'export REMOTE="s3://cc-port-dev?region=us-east-1&endpoint=http%3A%2F%2Flocalhost%3A9000&hostname_immutable=true&disable_https=true&s3ForcePathStyle=true"' Enter
Type 'export HOME="$SOURCE_HOME"' Enter
Type 'export PS1="me@laptop:~\$ "' Enter
Type 'clear' Enter
Type 'bash docs/videos/fixtures/seed-source.sh /Users/demo/source-project >/dev/null' Enter
Type 'clear' Enter
Show

# Source machine: push.
Type 'cc-port push /Users/demo/source-project --as project --remote "$REMOTE" --apply --all'
Sleep 500ms
Enter
Sleep 5s

# Swap to teammate.
Hide
Type 'export HOME="$TARGET_HOME"' Enter
Type 'export PS1="teammate@laptop:~\$ "' Enter
Type 'bash docs/videos/fixtures/seed-target.sh >/dev/null' Enter
Type 'clear' Enter
Show

# Teammate machine: pull.
Type 'cc-port pull project --to /Users/demo/teammate-project --remote "$REMOTE" --apply'
Sleep 500ms
Enter
Sleep 5s

Type 'ls ~/.claude/projects/'
Sleep 500ms
Enter
Sleep 3s
