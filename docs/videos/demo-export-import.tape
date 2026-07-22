# demo-export-import — the paired flow: export an archive on one machine,
# import it on another. Demonstrated with two distinct synthetic HOMEs and
# a PS1 swap to signal the second machine.

Output docs/images/demo-export-import.gif
Output docs/images/demo-export-import.mp4

Set Width 1650
Set Height 1000
Set FontSize 22
Set TypingSpeed 20ms
Set PlaybackSpeed 1.0
Set Padding 20

# Source-machine bootstrap.
Hide
Type 'export SOURCE_HOME="$(mktemp -d /tmp/ccp.XXXXXX)"' Enter
Type 'export TARGET_HOME="$(mktemp -d /tmp/ccp.XXXXXX)"' Enter
Type 'mkdir -p /tmp/cc-port-demo-share' Enter
Type 'export NEUTRAL_DIR="$(mktemp -d /tmp/ccp.XXXXXX)"' Enter
# TERM_PROGRAM=Apple_Terminal stops the ledger UI's terminal capability query;
# its reply would otherwise leak onto the next prompt as a "2026;0$y" residue.
Type 'export TERM_PROGRAM=Apple_Terminal' Enter
Type 'export HOME="$SOURCE_HOME"' Enter
Type 'export PS1="me@laptop:~\$ "' Enter
Type 'clear' Enter
# The source omits Codex's rebuildable state-DB cache, so export carries an empty
# threads sidecar and the teammate rebuilds its thread index from the rollout.
Type 'seed-home --home "$HOME" --project /Users/demo/source-project --role source --codex-state-db=false >/dev/null' Enter
Sleep 3s
Type 'clear' Enter
Sleep 200ms
Show

# Source machine: export.
Type 'cc-port export /Users/demo/source-project --output /tmp/cc-port-demo-share/project.zip --all'
Sleep 500ms
Enter
Sleep 2s

# Swap to the teammate HOME and prompt.
Hide
Type 'export HOME="$TARGET_HOME"' Enter
Type 'export PS1="teammate@laptop:~\$ "' Enter
Type 'seed-home --home "$HOME" --role target >/dev/null' Enter
Sleep 1s
Type 'clear' Enter
Sleep 200ms
Show

# Teammate machine: import, then let Codex converge its local state.
Type 'cc-port import /tmp/cc-port-demo-share/project.zip /Users/demo/teammate-project'
Sleep 500ms
Enter
Sleep 2.5s

# Move to a neutral git repo (Codex refuses to run outside one).
Hide
Type 'git init "$NEUTRAL_DIR"' Enter
Type 'cd "$NEUTRAL_DIR"' Enter
Type 'clear' Enter
Sleep 200ms
Show

Type "# Open Codex once so it rebuilds its session index from the imported sessions." Enter
Type "# No account here, so the model turn just errors; the reindex already committed." Enter
Sleep 500ms
Type 'codex exec "hello"'
Sleep 500ms
Enter
Sleep 3s

Type 'cc-port stats /Users/demo/teammate-project'
Sleep 500ms
Enter
Sleep 3s
