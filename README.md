# claude-topics

Pick up, put down, and hand off long-running Claude Code work — from anywhere,
including your phone.

One Go binary plus a Claude Code plugin:

| Piece | What it is |
|-------|------------|
| `topic` | Manage **topics**: stable names that outlive the session beneath them |
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

### Renaming

A *session* carries its name in five places, and nothing keeps them in step:

| where | set by |
|---|---|
| the tmux session | `tmux new-session -s` |
| the peer name in `ListAgents` | `--name` |
| the name in the Claude UI | `--remote-control`, pinned to a bridge |
| the transcript's `custom-title` | the session itself, appended on each rename |
| the topic registry | `topic` |

They drift apart the moment anything renames one of them in isolation — `/rename` in
the Claude UI, for instance, changes only the cloud name, leaving every local command
still using the old one.

This is the session-level problem a **topic** exists to solve. The topic name is the
one that is authoritative; the five above are just how a session happens to present
itself, and keeping them consistent is the tool's job rather than yours:

```bash
topic rename "<old>" "<new>"
```

That updates all five — the registry directory and `name`, the tmux session, the peer
name other sessions see in `ListAgents`, the Claude UI name (via a bridge re-mint),
and the transcript's `custom-title` so `adopt` can still find it later. `forkedFrom`/`forks`
references in other topics are repointed too, so lineage survives. A live topic is put
down and brought back up under the new name; `--dry-run` shows the plan.

`move` also **re-registers Remote Control**. A session's RC identity — the name shown
in the Claude UI *and the account it appears under* — is pinned to a bridge recorded
inside the transcript, and Claude Code rejoins that bridge on resume, so
`--remote-control "<name>"` is silently ignored for an existing session. Without this
a moved topic keeps advertising its old profile's account under a hostname-derived
name (`bretts-mac-studio-lan-…`). `move` drops those records so the next launch mints
a fresh bridge in the destination profile's account, named after the topic;
`--keep-bridge` opts out. `topic rebridge "<name>"|--all` does the same to a topic
that is already in the right place.

**The cost of a re-mint:** the *cloud* conversation restarts. The local transcript
keeps everything and the session resumes with its full history, but the Claude UI
shows the topic from the re-mint onwards, and the previous cloud entry is orphaned —
still listed, still renameable, no longer connected to anything. That orphan is what
you are looking at if a session in the UI shows different content from the live one.
Use `--keep-bridge` when continuous cloud history matters more than a correct name and
account. The transcript is copied to `<topic>/backups/`
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
  readiness marker that never arrives. `topic`, `topic cycle` and `install.sh` all
  special-case this.

`topic list` prints the profile whenever it is not the default, so a short list
cannot be mistaken for missing topics.

### Cycling everything — `topic cycle`

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

## Getting it

```bash
git clone https://github.com/verveguy/claude-topics.git ~/dev/claude-topics
cd ~/dev/claude-topics && ./install.sh
topic --help
```

`install.sh` builds `bin/topic` and links it, the plugin and a launchd agent into
place — the plugin as a **symlink back into the repo**, so editing a skill takes
effect immediately. Re-run it after changing anything under `cmd/`.

To install into a second Claude Code profile, point `CLAUDE_CONFIG_DIR` at it:

```bash
CLAUDE_CONFIG_DIR=~/.claude-work ./install.sh
```

`./install.sh --uninstall` removes the links and the launchd agent. It deliberately
leaves `<profile>/topics/` alone — that's your registry, handoff docs and briefs, not
the tool.

### What it assumes

- **macOS**, for the Dispatcher. `topic cycle` and the always-up Dispatcher use
  launchd (`~/Library/LaunchAgents`, `launchctl`). Everything else — up, down, fork,
  handoff, move, rename, adopt — is tmux and Claude Code only, so it would work on
  Linux with a systemd-user equivalent, which nobody has written.
- `tmux`, `claude`, `go`, `git`, and `~/.local/bin` on your `PATH`.
- A Claude Code profile that has completed first-run setup. `topic` refuses to
  automate one that has not, rather than hanging on the prompt — see Profiles.

Nothing else is machine-specific: the launchd label
(`io.github.verveguy.claude-dispatcher`) is a reverse-DNS namespace anchored to the
repo's GitHub account, and every path derives from `$HOME` and `CLAUDE_CONFIG_DIR` at
install time.

### Just the skills

The skills are also published as a plugin marketplace in this repo, so Claude can be
taught the procedures in another profile, or on a machine where the CLI already
exists:

```bash
claude plugin marketplace add verveguy/claude-topics
claude plugin install topics@claude-topics
```

They drive the `topic` CLI, so they are only useful alongside it. The plugin cannot
ship the binary — it is Go, so platform-specific, and committing a build would put
megabytes into every clone — and Claude Code has no postinstall hook. What it does
ship is `plugin/bin/topic-bootstrap`, which lands on the Bash tool's `PATH` while the
plugin is enabled:

```bash
topic-bootstrap             # what is installed, and what is missing
topic-bootstrap --install   # clone and build it, as above
```

The skills point at it when a `topic` command comes back "command not found". It is
deliberately not automatic: installing a binary and a launchd agent is the user's
decision, not a side effect of loading a skill.


## Under the hood

- [Non-obvious mechanics](docs/mechanics.md) — how Claude Code actually behaves:
  session records, Remote Control bridges, config-dir resolution, startup prompts.
  Read this first when something here misbehaves.
- [Development](docs/development.md) — implementation, the test suite, and layout.

## License

MIT — see [LICENSE](LICENSE).
