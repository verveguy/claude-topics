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

Only the session itself can write its handoff doc, so step 2 has to happen inside it —
but step 3 is what *ends* that session, and a command cannot wait for a result it will
never live to receive. Run from inside its own topic, `handoff-swap` therefore returns
at once and detaches: a separate process retires the session moments later and brings
the successor up. Run from anywhere else it behaves normally.

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
shows the topic from the re-mint onwards. Use `--keep-bridge` when continuous cloud
history matters more than a correct name and account. The transcript is copied to
`<topic>/backups/` first, since editing one is unsupported.

**The old cloud session is archived, if the topic is up.** Before `move`, `rename` or
`rebridge` drop a bridge, they disconnect it from inside the running session
(`/remote-control` → *Disconnect this session*). Claude Code then archives the cloud
session itself, with its own credentials, while it is still running as the source
account. A plain `down` does not do this — Claude Code keeps the cloud session for
resume and only marks it offline — so without the disconnect every re-mint left the
old account listing a dead session under the topic's name. A topic that is **down**
has no process to disconnect: its old session stays listed, and `topic` prints its
URL so you can archive it by hand. Topics re-minted before this change left such
orphans behind; they are still listed and must be archived separately.

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

### The rest

- **[Profiles](docs/guide.md#profiles)** — `topic` follows `CLAUDE_CONFIG_DIR`, so
  topics are per-profile, and `topic move` carries one between them: registry, docs,
  every transcript, and a re-minted Remote Control identity.
- **[The Dispatcher](docs/guide.md#the-dispatcher)** — one always-up topic per
  profile whose job is running `topic` commands for the others, so you can start work
  remotely when nothing is up. It runs in `<profile>/dispatcher/` unless its registry
  entry or `CLAUDE_DISPATCHER_DIR` says otherwise — deliberately not `$HOME`, where an
  unqualified search walks every TCC-protected folder you own and raises permission
  dialogs blaming `topic`. Where it runs does not constrain where it starts topics.
- **[Cycling everything](docs/guide.md#cycling-everything-topic-cycle)** —
  `topic cycle down` / `up` takes every profile's topics down and back, launchd agents
  included, for logging in and out or upgrading.
- **[Adopting](docs/guide.md#adopting-an-existing-session)** — bring a session
  `topic` did not launch under management, live or merely resumable.

### Daemons — keeping non-topic processes up

Long-running processes that are not Claude topics, but live like them in their own
detached tmux session (the Fabrik engines and the Pruefer reviewers), can be registered as
**daemons**. The Dispatcher's five-minute launchd cycle then keeps them up too:

```bash
topic daemon add "liminis-daemon" ~/dev/liminis-project daemon-env fabrik --auto-upgrade
topic daemons                       # each daemon: running, husk (pane back at a shell), or missing
topic daemon stop "liminis-daemon"  # stop on purpose; the keeper leaves it down
topic daemon start "liminis-daemon" # and back
topic ensure-daemons                # just the keeper pass, by hand
```

On each `ensure-dispatcher` run, a registered daemon whose session is **missing** is created
with its command, and one whose pane is **back at a shell** has its command typed in again.
A pane counts as back at a shell only when its shell is in the foreground *and* has no
child processes, so a daemon running under a wrapper script that doesn't `exec` is not
mistaken for a husk. The flip side: a husk whose shell still has a leftover background
job is treated as running and not restarted.

`daemon add` takes the command either as **one** argument, a complete shell line stored
verbatim (`topic daemon add x ~/d "sh -c 'fabrik --name \"a b\"'"`), or as **several**,
an argv that is shell-quoted word by word so it means exactly what you passed. A daemon marked **stopped** is left alone. The registry is per profile
(`<config dir>/daemons.json`), because the agent that runs the check is per profile; a
daemon's own `.env` pins the Claude profile its workers use (`daemon-env`), so register it
under any profile whose Dispatcher agent is loaded. Each start appends
`echo "[topic] daemon <name> exited with status $?"`, so an exit is visible in the pane:
daemons have exited without logging why.

## Getting it

```bash
git clone https://github.com/verveguy/claude-topics.git ~/dev/claude-topics
cd ~/dev/claude-topics && ./install.sh
topic --help
```

`install.sh` builds `bin/topic` and links it, the plugin and a launchd agent into
place — the plugin as a **symlink back into the repo**, so editing a skill takes
effect immediately. Re-run it after changing anything under `cmd/`.

**Build with `make build` or `./install.sh`, never a bare `go build`** — see below.

To install into a second Claude Code profile, point `CLAUDE_CONFIG_DIR` at it:

```bash
CLAUDE_CONFIG_DIR=~/.claude-work ./install.sh
```

`./install.sh --uninstall` removes the links and the launchd agent. It deliberately
leaves `<profile>/topics/` alone — that's your registry, handoff docs and briefs, not
the tool.

### Why the binary is code-signed

Build with `make build` (or `./install.sh`, which calls the same script). A bare
`go build` produces a binary that works fine and then, days later, buries you in macOS
permission dialogs you cannot get rid of.

The symptom is startling, because `topic` appears to be asking for things it has no
business wanting:

> **"topic" would like to access data from other apps.**

and the same for your photo library, your Documents folder, and all your files. `topic`
reads nothing outside `~/.claude*` and its own registry, so this looks like malware.
It isn't. Two mechanisms compose:

**1. topic becomes the responsible process for everything in tmux.** launchd starts
`topic ensure-dispatcher` at login, before any terminal is open — so `topic` is what
creates the tmux server. macOS attributes a permission request to the *responsible*
process, the ancestor that started the chain, and the children here are unsigned
interpreters (`node`, `sh`, `find`) with no identity of their own. So every request
made by any Claude session, MCP server, hook, or `find` a session runs arrives wearing
topic's name. An MCP server wants the microphone; a session greps from `$HOME` and
walks into `~/Pictures`; the dialog says `topic`. You never see this before a reboot,
because your terminal emulator started the tmux server and already held those grants.

**2. Clicking the dialog does nothing.** The Go linker signs ad-hoc with
`Identifier=a.out`. That is not a unique identity — every ad-hoc Go binary on the
machine claims it — so TCC will not persist a decision against it. The stored answer
stays "unknown" and the dialog returns forever, whether you clicked Allow or
Don't Allow.

`scripts/build.sh` signs with a stable identifier (`io.github.verveguy.topic`), which
is what lets one answer stick. It also stamps that identifier in at link time, so the
binary can tell whether it was built correctly: `topic up`, `topic cycle` and
`topic ensure-dispatcher` warn on stderr when it wasn't, and `topic doctor` explains
what to do.

```bash
topic doctor      # what macOS thinks this binary is, and how to fix it
```

**Answer the dialog with Don't Allow.** A denial persists exactly as a grant does, and
a grant to `topic` is inherited by everything it spawns — every session, worker and MCP
server. Full Disk Access on `topic` is Full Disk Access for all of them. If some tool
genuinely needs a protected path it will fail under its own name, and you can grant
that app directly.

If you have already been living with this, the signed binary alone won't settle it:
the running tmux server predates it, and its sessions are still parented onto the old
one. From a terminal **outside tmux** — and note `kill-server` ends every session,
including any daemons you run there:

```bash
topic cycle down
tmux kill-server
tccutil reset All io.github.verveguy.topic
topic cycle up
```

To sign with a real Developer ID instead of ad-hoc:

```bash
TOPIC_SIGN_IDENTITY="Developer ID Application: You (TEAMID)" make build
```

### What it assumes

- **macOS**, for the Dispatcher. `topic cycle` and the always-up Dispatcher use
  launchd (`~/Library/LaunchAgents`, `launchctl`). Everything else — up, down, fork,
  handoff, move, rename, adopt — is tmux and Claude Code only, so it would work on
  Linux with a systemd-user equivalent, which nobody has written.
- `tmux`, `claude`, `go`, `git`, `make`, and `~/.local/bin` on your `PATH`. Also
  `codesign`, from the Xcode command line tools — the build refuses to produce an
  unsigned binary rather than hand you one that nags forever.
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
- [Guide](docs/guide.md) — profiles, the Dispatcher, cycling, adoption.
- [Development](docs/development.md) — implementation, the test suite, and layout.

## License

MIT — see [LICENSE](LICENSE).
