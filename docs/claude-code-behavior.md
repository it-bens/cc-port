# Claude Code file-write behavior

Claude Code is closed source. cc-port rewrites files that Claude Code also writes, so what happens when both write at once can only be established by observing Claude Code. This document records those observations as experiments: the question each one answers, how it was run, what was observed, and what follows for cc-port.

## Summary

| # | Question | Result |
|---|---|---|
| 1 | How does Claude Code write `history.jsonl` and `.claude.json`? | Both writes run under a lock directory; `.claude.json` is also written twice on exit without one |
| 2 | Does Claude Code keep `history.jsonl` open between appends? | No. Each append opens and closes the file |
| 3 | Does Claude Code write a stale copy of `.claude.json` back? | No. Every write merges into the content on disk |
| 4 | Where is the config lock without a custom config directory? | `.claude.json.lock` next to `~/.claude.json` |
| 5 | Which other Claude files are written under a lock? | `known_marketplaces.json` only; `settings.json` and `installed_plugins.json` are not |
| 6 | What does Claude Code do when its lock is held by someone else? | It defers the write to the next append, and removes a lock with an old mtime |

## Materials and methods

### Subject

Claude Code v2.1.284 on macOS, signed in with a subscription account. Every result applies to this version only. The lock protocol and write patterns are internal to Claude Code and not a documented contract.

### Isolated configuration

Experiments 1, 2, 3, 5 and 6 ran against a scratch configuration directory selected with `CLAUDE_CONFIG_DIR`, with a scratch project as the working directory. With that variable set, Claude Code keeps `.claude.json`, `history.jsonl`, `settings.json` and the `plugins` directory inside the scratch directory, so no experiment touched the real configuration. For Experiments 5 and 6, the session was started with a minimal environment, so it inherited no settings or credentials from the shell that launched it.

Experiment 4 observed the real configuration without changing it.

### Instruments

- **System-call tracer.** `fs_usage -w -f filesys <pid>`, which requires root, recorded every file-system call of one Claude Code process: opens with their flags, reads, writes, renames, and the creation and removal of directories.
- **Lock watcher.** A small script that repeatedly lists the configuration directory and reports each `*.lock` entry as it appears and disappears, with a timestamp. A second version also reports when a watched file is replaced, detected as a change of inode. It needs no root. Locks held for as little as 0.5 ms were detected, so the sampling interval was shorter than that. A lock held for a shorter time than one sampling cycle can be missed.
- **Probes.** Direct changes to the files under test (replacing a file, adding a key, creating a lock directory), followed by a prompt or command in a running session, and a check of the file afterwards.

## Experiment 1: write pattern

**Question.** Which system calls does Claude Code make when it writes `history.jsonl` and `.claude.json`?

**Procedure.** The tracer ran against a scratch session while it answered a prompt and then exited.

**Observations.** One append to `history.jsonl`:

```
mkdir   history.jsonl.lock
open    history.jsonl   (write, create, append)
write
close
rmdir   history.jsonl.lock
```

A write of `.claude.json` during the session:

```
mkdir   .claude.json.lock
open    .claude.json    (read)
write   backup copy into the backups directory
write   temporary file next to .claude.json
rename  temporary file -> .claude.json
rmdir   .claude.json.lock
```

On exit, `.claude.json` was written twice more. Each write read the file and replaced it through a temporary file and a rename about 3 ms later. Neither took `.claude.json.lock`, and neither wrote a backup.

Throughout the session, Claude Code checked the metadata of `.claude.json` once per second.

**Result.** Both files are written under a lock directory named after the file, taken with `mkdir` and released with `rmdir`. `.claude.json` is replaced through a rename, and its two exit writes take no lock. The once-per-second check suggests Claude Code watches the config for outside changes; the trace does not show what it does when one is found.

## Experiment 2: history file handle

**Question.** Does Claude Code hold `history.jsonl` open between appends? If it does, a rename over the file sends every later append to the replaced file, and those appends are lost.

**Procedure.** During a running session, `history.jsonl` was replaced by a copy of itself, which gives it a new inode, the same effect as cc-port's rename. The inode was compared before and after. A prompt with a unique marker was then sent, and the new file was searched for the marker.

**Observations.** The inode changed. The marker appeared in the new file.

**Result.** Claude Code does not hold `history.jsonl` open. An outside rename can lose only an append that lands between the other writer's read and its rename. This agrees with the trace in Experiment 1, where every append opens and closes the file.

## Experiment 3: config write-back

**Question.** Does Claude Code keep `.claude.json` in memory and write that copy back? If it does, a running session undoes any outside change to the file, including a project rename made by cc-port.

**Procedure.** A session was started. While it ran, a top-level marker key and a fake project entry were added to `.claude.json` from outside. The session then answered a prompt and exited, and the file was checked for both additions.

A first run was discarded. In it, the additions were made while no session was running, so the next session loaded them at startup, and their survival said nothing about write-back.

**Observations.**

- The backup Claude Code wrote during the session does not contain the marker, which places the addition after session start.
- The last write of the file coincides with the exit.
- After exit, the file holds both additions and the session's own exit data.

**Result.** Claude Code merges its changes into the content on disk. A running session does not revert an outside change to `.claude.json`. This agrees with Experiment 1, where every write reads the file first.

## Experiment 4: lock location in the default configuration

**Question.** Where does the config lock live when no custom configuration directory is set?

**Procedure.** The lock watcher observed the home directory, the `.claude` directory and its `plugins` directory for 100 seconds while regular Claude Code sessions ran. Nothing was changed.

**Observations.** A `.claude.json.lock` directory appeared next to `~/.claude.json` and was removed about 4 ms later. No history write happened during the observation.

**Result.** The config lock is `~/.claude.json.lock`. The rule seen in all experiments is a lock directory named `<file>.lock` next to the file. The history lock location, `~/.claude/history.jsonl.lock`, follows from that rule but was not observed directly.

## Experiment 5: locks on plugin and settings files

**Question.** Are the other Claude files cc-port rewrites written under a lock?

**Procedure.** A plugin was installed and then uninstalled with the Claude Code CLI against the scratch configuration. Both runs were repeated with the extended watcher, which also reports replaced files.

**Observations.**

| File | Written by | Lock |
|---|---|---|
| `plugins/known_marketplaces.json` | install | `known_marketplaces.json.lock`, held under 1 ms around the rename |
| `settings.json` | install, uninstall | none; replaced through a rename |
| `plugins/installed_plugins.json` | install, uninstall | none; replaced through a rename |

During the uninstall, a lock directory `.storage-write.lock` appeared in the configuration directory about 2 ms after `settings.json` and `installed_plugins.json` had been replaced, followed by a `.claude.json.lock`.

**Result.** `known_marketplaces.json` follows the same lock protocol. `settings.json` and `installed_plugins.json` are replaced without a lock. The file `.storage-write.lock` guards is not known; it was not held while either of the two files was written.

## Experiment 6: contended and stale locks

**Question.** What does Claude Code do when its history lock is already held by another process?

**Procedure.**

1. **Held lock.** `history.jsonl.lock` was created by hand with a current mtime. A prompt with a unique marker was sent. The lock and the history file were checked every 5 s for 60 s. The lock was then removed, and after a further 10 s a second prompt with another marker was sent.
2. **Old lock.** `history.jsonl.lock` was created by hand with its mtime set one hour in the past. A prompt with a unique marker was sent. The lock and the history file were checked every 5 s for 20 s.

**Observations.**

1. With the held lock, the session answered the prompt normally, with no error and no delay. The lock stayed in place for all 60 s, and the marker did not appear in `history.jsonl`. Ten seconds after the lock was removed, it still had not appeared. After the second prompt, both markers were in the file, in order.
2. With the old lock, the lock was gone and the marker was in the file at the first check, 5 s after the prompt.

**Result.** Claude Code does not wait for a held lock and does not drop the entry. It keeps the entry in memory and writes it with the next append that gets the lock. A lock with an old mtime is treated as stale and removed. The age at which a lock counts as stale lies between about 1 s, at which the held lock was not removed, and one hour.

## Relevance to cc-port

cc-port's move rewrites `history.jsonl` and `.claude.json` by reading the file, rewriting it in memory and renaming a new file over it.

- Experiments 2 and 3 limit what a running Claude Code session can lose during a move. It cannot write to a file cc-port has replaced, and it cannot revert cc-port's change afterwards. An append or config write that lands between cc-port's read and its rename is lost.
- Experiments 1, 4, 5 and 6 show that cc-port could take Claude Code's own lock for `history.jsonl`, `.claude.json` and `known_marketplaces.json` around its read and rename. Claude Code would then defer its history write instead of losing it. The two lockless exit writes of `.claude.json`, and all writes of `settings.json` and `installed_plugins.json`, stay outside that protection. The change is tracked in #105.

## Open questions

- Contention on `.claude.json.lock` was not tested. Experiment 6 covered only the history lock.
- The exact age at which a lock counts as stale.
- Whether a history entry deferred by a held lock is written when the session exits before its next append.
- What Claude Code does after its once-per-second check finds that `.claude.json` changed.

## Reproducing the experiments

Run every experiment that changes files against a scratch configuration directory selected with `CLAUDE_CONFIG_DIR`, never against the real configuration. Re-run the experiments after a Claude Code update that changes how these files are written, and update the version under Materials and methods.
