# Guide

The parts of `topic` you reach for less often than picking a topic up and putting
it down: running more than one Claude Code profile, moving work between them, the
always-up Dispatcher, taking everything down cleanly, and bringing sessions
`topic` did not launch under management.

## Profiles

`topic` honours **`CLAUDE_CONFIG_DIR`**, exactly as `claude` does. If you run more
than one Claude Code profile, set it and everything follows — registry, session
records, transcripts, and the profile the launched session itself runs under:

```bash
CLAUDE_CONFIG_DIR=~/.claude-work topic list
CLAUDE_CONFIG_DIR=~/.claude-work ./install.sh   # install into that profile too
```

Topics are per-profile, and the tool can see across all of them:

```bash
topic profiles                        # every profile, and how many topics it holds
topic list --all                      # every topic in every profile
topic move "<name>" --to work         # move a topic into another profile
topic move "<name>" --to default --dry-run
```

**Per-project approvals do not travel with a topic.** Whether a directory is trusted,
and whether its CLAUDE.md may import files from outside it, is recorded per profile —
so a topic can arrive somewhere it is not allowed to start. `move` checks before it
moves anything: if the source profile approved external imports for that directory
and the destination has not, it stops and tells you how to answer it. The source
having approved is precisely the signal that the destination will be asked, since the
flag only becomes true by being answered.

`--carry-approvals` copies your existing answers for that one directory into the
destination instead. It is deliberately limited to the trust and external-import
answers: the same project entry also holds `allowedTools`, and copying a permission
allowlist into a profile that never granted it would be a silent escalation. It also
writes to the destination profile's `.claude.json`, which running sessions write too,
so prefer it when that profile is quiet. `--force` moves anyway and leaves the topic
down.

If a live move does fail to restart, the move itself has still succeeded — the topic
is down, resumable, and reported as such, rather than the failure reading as a lost
move.

## The Dispatcher

An always-up topic whose only job is running `topic` commands for the others. It
solves the bootstrapping problem: you can't `topic up` remotely if nothing is up.

Kept alive by launchd — at login, plus a check every 5 minutes calling
`topic ensure-dispatcher` (idempotent). Supervision goes through that check rather
than `KeepAlive` because the real process lives inside detached tmux, which launchd
cannot watch directly.

```bash
topic ensure-dispatcher              # any session can revive it
tail -f ~/Library/Logs/claude-dispatcher.log
```

From your phone, you talk to the Dispatcher: *"pick up Fantasy Economics"*,
*"put down Fantasy Images"*, *"what topics are up?"*

## Cycling everything — `topic cycle`

To log a profile in or out, rotate credentials, or upgrade Claude Code, you want
everything down and then back. Two commands, because the point is what you do in
between:

```bash
topic cycle down           # unload agents, put every topic in every profile down
# ...log each profile in from a plain session in ~ , check /status...
topic cycle up             # reload agents, start each profile's Dispatcher
topic up "<name>"          # pick topics back up as you want them
```

Both take `--dry-run`. `topic cycle profiles` lists what it will act on — the
installed Dispatcher plists *are* that list, since each names the profile it manages,
so there is no second list to keep in sync.

The ordering is the entire reason this exists:

- **Unload the launchd agents first.** They run `ensure-dispatcher` every 5 minutes,
  so a Dispatcher put down without unloading its agent comes back underneath you —
  typically in the middle of logging in.
- **Putting topics down is lossless.** Each keeps its session UUID; `up` is a real
  `--resume`. Nothing here destroys work, so `down` is safe to run freely.
- **The calling session is skipped**, since putting it down would kill the script
  mid-run. It is named at the end so you can take it down last by hand.

## Adopting an existing session

A session started outside `topic` can be brought under management. Every live
session publishes `~/.claude/sessions/<pid>.json` — the authoritative
`name` → `sessionId` → `cwd` mapping — so adoption normally needs no probe and no
round trip:

```bash
topic adopt "<name>"                 # matches a live session whose name is <name>
topic adopt "<name>" --pid 61222     # or point straight at one
topic adopt "<name>" --dir ~/dev/x   # or narrow by working directory
```

Candidates are narrowed by `--pid`, then `--dir`, then by a name equal to the topic
name; if that doesn't leave exactly one, `topic` lists the live sessions with the
`--pid` needed to disambiguate. Records linger after a session exits, so liveness is
re-checked against the pid.

**A session that has exited can be adopted too** — anything `claude --resume` lists.
Its name is in its own transcript, as `custom-title` records, which is where
`--resume` reads it from. Same command; `topic` falls through to this automatically
when nothing live matches:

```bash
topic adopt "Fantasy UX"                      # a resumable session, not running
topic adopt "<name>" --title "<resume name>"  # topic name differs from the title
```

Sessions get **renamed**, and every rename stamps a new title, so only the *last* one
counts — a session titled "Fantasy UX" early on and "Fantasy OpenAI" now is not a
match. If several sessions genuinely share the title, `topic` lists them newest-first
with the `--session-id` to pick one. There is no process to claim, so the topic lands
in the ordinary `down` state and `topic up` simply resumes it.

The scan greps every transcript (~5s over 800MB here), so it is deliberately the
fallback, not the first thing tried.

`--probe` remains for a session that is running but *unnamed* — nothing to match on
in either direction. A running session doesn't hold its transcript open, so the probe
path sends it a unique token and finds which transcript that token landed in:

```bash
topic adopt "<name>" --probe <token> --exclude <your-own-session-uuid>
```

`--session-id <uuid>` takes a UUID you already know — a session adopting *itself*, or
your pick from an ambiguous list. It works for live and exited sessions alike:
`topic` checks whether that UUID is currently running and registers it accordingly.

`adopt` is non-destructive — it only records the UUID, directory, and pid — and
`--dry-run` reports what it would adopt without registering anything, which is the
safe way to answer "is this session already a topic, and which one?".

**Then claim it.** The adopted session is still running outside tmux. Starting it
under `topic` would resume the same session UUID in a *second* process — two writers
on one transcript, which corrupts it.

```bash
topic up "<name>"           # after adoption via a live record
topic up "<name>" --claim   # after --probe / --session-id adoption
```

This step only applies to a session that is actually **running**. Adopting a
resumable, exited session needs no claim at all — it is already `down`.

Adoption through a live record stores the pid, so `topic up` can *see* the original:
it refuses outright while that process is alive (`--claim` will not override it), and
needs no assertion once it's gone. The probe and `--session-id` paths have no pid to
check, so they still fail closed until you assert the original has exited with
`--claim`.
