# claude-topics

Pick up, put down, and hand off long-running Claude Code work — from anywhere,
including your phone.

Two scripts plus a Claude Code plugin:

| Piece | What it is |
|-------|------------|
| `topic` | Manage **topics**: stable names that outlive the session beneath them |
| `topics-cycle` | Take every profile's topics down cleanly, and bring them back |
| `topics` plugin | Skills (`handoff`, `fork`, `topic-manage`) so Claude knows the procedures |

## The problem this solves

You leave Claude Code sessions running for a week so you can continue the dialogue
from your phone while travelling. Two things go wrong:

1. **Context rots.** A week-old session is cluttered with detail that no longer
   matters, but you don't want to lose the thread.
2. **You can't start new work remotely.** If you need a session that isn't already
   running, you're stuck until you're back at the machine.

`topic` fixes both, by making the unit of work a *topic* rather than a session — the name stays stable while the session underneath is resumed, retired,
or replaced.

## Install

```bash
git clone <this repo> ~/dev/claude-topics
cd ~/dev/claude-topics
./install.sh
```

Everything installs as a **symlink back into the repo**, so editing a script here
takes effect immediately. Requires `tmux`, `claude`, `python3`, and `~/.local/bin`
on your `PATH`.

`./install.sh --uninstall` removes the links and the launchd agent. It deliberately
leaves `~/.claude/topics/` alone — that's your registry and handoff docs, not the tool.

## `topic` — the tool

```bash
topic up     "<name>" [dir]   # pick up: resume the topic's session, or start one
topic down   "<name>"         # put down: terminate, but stay resumable
topic list                    # what's up, what's down
topic status "<name>"         # dir, session UUID, generation, retired sessions
topic forget "<name>"         # stop tracking it (keeps handoff docs)
topic prune                   # show topics that are dead; --yes removes them
topic down-all                # put every live topic in this profile down

topic up "<name>" [dir] --seed-from <file>    # start a NEW topic from a brief
```

`--seed-from` seeds a brand-new topic with a document — a handoff doc, an `ideas/`
note, an issue write-up. Ignored (with a warning) if the topic already has a session
to resume. For splitting a thread out of a topic you are *in*, use `fork` below,
which wraps this and records the lineage.

`forget` is the disposal path for one topic you have decided you are done with. It
refuses while the topic is up, keeps the handoff documents unless you pass `--purge`
(they are often the only surviving record of what a topic was about), and never
touches the session itself — it stays resumable with `claude --resume <uuid>`.

`prune` is the disposal path for topics that are dead whether you decided so or not:
down, with no transcript left in `~/.claude/projects`, so `up` could never resume
them. It is a **dry run by default** — it prints what it would remove and stops.
`--yes` carries it out (via `forget`, so handoff docs survive unless you add
`--purge`). Live and merely-put-down topics are never touched.

```bash
topic prune                   # what is dead?
topic prune --yes             # remove them, keep their handoff docs
topic prune --yes --purge     # remove them and their handoff docs
```

```bash
ssh home -t tmux attach -t "<name>"       # attach to any topic over SSH
```

Each topic is backed by a stable session UUID, so `up` after `down` is a genuine
`--resume` — **lossless**. Putting a topic down is not destructive; do it freely.

Topics are singletons: `topic up` on a live topic is a no-op, not an error.

### Two access paths

Every session is launched with **both** `--remote-control "<name>"` and
`--name "<name>"`, giving two independent ways in:

| Path | Reach |
|------|-------|
| tmux | local attach, SSH attach, survives disconnect |
| Remote Control | claude.ai / phone, **and** other Claude sessions via `ListAgents` / `SendMessage` |

### Hand off — clearing context without losing the thread

Retires the current session and starts a fresh one under the same topic name, seeded
with a handoff document:

```bash
p=$(topic handoff-path "<name>")   # 1. where the doc goes
# 2. the session writes its handoff doc to $p
topic handoff-swap "<name>" --focus "the thing to pick up first"
```

In practice you don't run these by hand — you say `/handoff` (or
`/handoff <focus>`) and the `handoff` skill drives it.

`--focus` scopes what the successor starts on. Off-focus work is still recorded as
open threads, so nothing is silently dropped: an omitted thread is indistinguishable
from a finished one.

Handoff docs are kept permanently in `~/.claude/topics/<slug>/handoffs/`. Retired
session UUIDs are archived in the topic's `history`, so an old session is still
resumable in an emergency.

### Fork — split a thread into its own topic

A topic that has grown two subjects should be two topics. `fork` takes one thread out
of the topic you are in and gives it its own name, session, and context — while the
parent keeps running.

```bash
# from inside the session being split
p=$(topic fork-path "Fantasy Images")   # where the brief goes
# ...write the brief to $p...
topic fork "Fantasy Images" --from "Fantasy Economics"
```

The `fork` skill drives this from `/fork "<Topic Name>" <what to extract>` — the
session writes the brief itself, since only it holds the context.

The pieces `--seed-from` alone left to convention:

- **Lineage is recorded both ways.** The child gets `forkedFrom` and `seededFrom`,
  the parent gains the child in `forks`. `topic status` shows both.
- **The child inherits the parent's working directory** unless you pass one. Without
  that it would land in `$PWD` — which, when you fork remotely through the
  Dispatcher, is the Dispatcher's home rather than the work.
- **`--from` defaults to the calling topic**, via `topic whoami`.

Briefs live permanently in `~/.claude/topics/<slug>/briefs/`, kept separate from
`handoffs/` because they are a different genre: a handoff doc looks back over a
topic's own history, a brief looks forward and is the child's origin document. It has
to carry everything — the child has no shared history to fall back on.

Forking does **not** remove the thread from the parent's context; it only stops the
parent being the only place it lives. Hand the parent off afterwards if it should
start clean. `topic fork` prints that command rather than running it, since it would
terminate the session you are sitting in.

```bash
topic whoami          # which topic am I in? (reads the tmux session name)
```

### Profiles

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
topic move "<name>" --to v3rv         # move a topic into another profile
topic move "<name>" --to default --dry-run
```

`move` also **re-registers Remote Control**. A session's RC identity — the name shown
in the Claude UI *and the account it appears under* — is pinned to a bridge recorded
inside the transcript, and Claude Code rejoins that bridge on resume, so
`--remote-control "<name>"` is silently ignored for an existing session. Without this
a moved topic keeps advertising its old profile's account under a hostname-derived
name (`bretts-mac-studio-lan-…`). `move` drops those records so the next launch mints
a fresh bridge in the destination profile's account, named after the topic;
`--keep-bridge` opts out. `topic rebridge "<name>"|--all` does the same to a topic
that is already in the right place. The transcript is copied to `<topic>/backups/`
first, since editing one is unsupported.

`move` takes the whole topic: registry entry, handoff docs, briefs, and **every
transcript it can still resume** — the current session and every retired one in its
`history`. The transcripts are the part that is easy to forget: `topic up` resumes by
UUID and `claude --resume` only looks inside its own profile's `projects/`, so a
registry moved on its own arrives unresumable, and leaving history behind quietly
breaks the emergency path back to a pre-handoff session.

A topic that is **up** is put down, moved, and brought back up on the far side —
both halves are lossless, so the session continues where it left off, now in the new
profile (`--stay-down` leaves it down). It refuses to move the topic you are *running
the command from*, since putting that down kills the command mid-move; run it from
another session, which is what the Dispatcher is for. It also refuses to overwrite an
existing topic of the same name in the destination.
`--copy` leaves the original in place; be aware that resuming both copies afterwards
forks the transcript. The destination records `movedFrom`/`movedAt`, since after a
move the old profile has no trace of where the topic went.

Three things make profile separation safe rather than merely possible:

- **Sessions are launched with the profile pinned** to the command line. A detached
  tmux session does not reliably inherit the environment — the tmux server may long
  predate it — and a session started under the wrong profile writes its transcript
  where `topic` cannot see it, breaking `adopt`, `prune`, and resume in ways that
  look like data loss.
- **tmux session names are namespaced** for non-default profiles (`work/Fantasy
  Images`), because tmux is machine-global. Without that, two profiles running a
  topic of the same name fight over one session and `topic up` reports the *other*
  profile's session as already up. The default profile keeps bare names, so existing
  sessions and `tmux attach -t "<topic>"` are unaffected.
- **The launchd agent is per-profile** — its own label, log, and a baked-in
  `CLAUDE_CONFIG_DIR`. launchd starts with a bare environment, so without that the
  Dispatcher would manage the default profile whichever profile installed it.
- **The default profile runs with `CLAUDE_CONFIG_DIR` unset, never set to
  `~/.claude`.** They are not equivalent: unset reads `~/.claude.json`, set reads
  `~/.claude/.claude.json` — a different file that has never completed onboarding, so
  the session comes up at the first-run theme picker and `topic` hangs waiting for a
  readiness marker that never arrives. `topic`, `topics-cycle` and `install.sh` all
  special-case this.

`topic list` prints the profile whenever it is not the default, so a short list
cannot be mistaken for missing topics.

### Cycling everything — `topics-cycle`

To log a profile in or out, rotate credentials, or upgrade Claude Code, you want
everything down and then back. Two commands, because the point is what you do in
between:

```bash
topics-cycle down          # unload agents, put every topic in every profile down
# ...log each profile in from a plain session in ~ , check /status...
topics-cycle up            # reload agents, start each profile's Dispatcher
topic up "<name>"          # pick topics back up as you want them
```

Both take `--dry-run`. `topics-cycle profiles` lists what it will act on — the
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

### The Dispatcher

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

### Adopting an existing session

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

`adopt` is non-destructive — it only records the UUID, directory, and pid.

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

## Non-obvious mechanics

Learned the hard way; don't re-derive them.

- **`--remote-control` alone does not name a session.** A session started with only
  `--remote-control "X"` advertises itself to peers as an auto-derived `<dir>-<hash>`
  and is unaddressable by name. You need `--name "X"` as well. `topic` passes both.
- **`SendMessage` resolves against the last `ListAgents` snapshot.** A freshly
  launched session is unreachable until you call `ListAgents` again. Call it first,
  then send — and expect to need the `[ref]` suffix.
- **Remote Control names live in the cloud, not on disk.** When such a session dies,
  its entry can linger in `ListAgents` as a ghost that accepts messages and never
  answers. Sessions launched by `topic` don't have this problem: `--name` writes the
  name to `~/.claude/sessions/<pid>.json`.
- **`~/.claude/sessions/<pid>.json`** is the authoritative live-session record —
  `name`, `sessionId`, `cwd`, `tmux`. Best starting point for any "which session is
  this?" question, and what `topic adopt` reads. Records are **not** removed when a
  session exits, so always re-check the pid before trusting one.
- **`CLAUDE_CONFIG_DIR=~/.claude` is not the same as leaving it unset.** The
  *directories* are identical — same `projects/`, `sessions/`, `topics/` — but the
  **config file** is not: unset reads `~/.claude.json`, set reads
  `~/.claude/.claude.json`. Those carry onboarding state, account, `userID` and
  `machineID`, so setting it to the default directory silently selects a *second
  identity over the same data*, typically one that has never completed onboarding.
  That is why the failure is so quiet: transcripts and sessions resolve correctly
  either way. Treat "default profile" as "variable absent", never "variable set to
  the default", and read a profile's config from `~/.claude.json` for the default
  profile and `<dir>/.claude.json` for every other.
- **A profile that has not completed first-run setup cannot be automated.** It opens
  on the theme picker (or a login prompt), which `topic` deliberately will not answer
  — theme and account are the user's choices. It detects that text, kills the session
  it just started, and says so, rather than timing out and leaving a session parked at
  a prompt that `is_up` would report as UP. Create a profile by hand first:
  `CLAUDE_CONFIG_DIR=~/.claude-new claude`.
- **An exiting session keeps writing briefly.** `topic down` returns once claude has
  left the pane, but the process can still flush a final `file-history-snapshot`
  record afterwards — which recreates the transcript at its old path after a move.
  Wait for the file to stop changing before relocating it.
- **A session's name survives its process, inside its own transcript.** Transcripts
  carry `{"type":"custom-title","customTitle":"..."}` records — that is the name
  `claude --resume` lists. A **rename appends another one**, so the *last* record is
  the current name and earlier ones are names since abandoned. They also record their
  own `cwd`, which is the right way to recover a session's directory: de-mangling the
  `~/.claude/projects` slug is ambiguous for any hyphenated directory
  (`-Users-me-dev-claude-topics`).
- **`pipefail` + `set -e` turns a missing directory into a silent abort.** `ls`/`find`
  over an absent path exits non-zero inside a command substitution, and the script
  dies with no output and nothing done. `topic forget` failed exactly this way for
  every topic that had no handoff docs — it looked like a no-op. Terminate such
  pipelines with `|| true`.
- **Session start-up detection is text-matching.** `topic` polls the pane
  for the trust prompt or a readiness banner (`Welcome back` / `remote-control is
  active`). Claude Code's startup text has changed twice already — suspect this first
  if launches hang for the full timeout.

## Layout

```
bin/topic                  topic manager
bin/topics-cycle           take everything down / bring it back, across profiles
plugin/                    the `topics` Claude Code plugin -> ~/.claude/skills/topics
  skills/handoff/          how to hand a topic to a fresh session
  skills/fork/             how to split a thread into its own topic
  skills/topic-manage/     pick up / put down / adopt / Dispatcher
launchd/                   Dispatcher agent template (__HOME__ substituted at install)
install.sh                 symlink installer
```

Runtime state lives outside the repo, in `~/.claude/topics/<slug>/`:
`topic.json` (name, dir, current session UUID, generation, history) and
`handoffs/*.md`.
