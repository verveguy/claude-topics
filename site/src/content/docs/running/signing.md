---
title: Code signing and permission dialogs
description: Why topic is code-signed, and what to do when macOS keeps asking for permissions in its name.
---

Build with `make build` or `./install.sh`, which both use `scripts/build.sh`. A bare `go build`
produces a binary that works fine and then, days later, buries you in macOS permission dialogs
you cannot get rid of.

The symptom is startling, because `topic` appears to be asking for things it has no business
wanting:

> **"topic" would like to access data from other apps.**

and the same for your photo library, your Documents folder, and all your files. `topic` reads
nothing outside `~/.claude*` and its own registry, so this looks like malware. It isn't. Two
mechanisms compose.

## 1. topic is the responsible process for everything in tmux

launchd starts `topic ensure-dispatcher` at login, before any terminal is open, so `topic` is
what creates the tmux server. macOS attributes a permission request to the *responsible* process,
the ancestor that started the chain, and the children here are unsigned interpreters (`node`,
`sh`, `find`) with no identity of their own. So every request made by any Claude session, MCP
server, hook, or `find` a session runs arrives wearing `topic`'s name. An MCP server wants the
microphone; a session greps from `$HOME` and walks into `~/Pictures`; the dialog says `topic`. You
never see this before a reboot, because your terminal emulator started the tmux server and
already held those grants.

## 2. Clicking the dialog does nothing

The Go linker signs ad-hoc with `Identifier=a.out`. That is not a unique identity (every ad-hoc Go
binary on the machine claims it), so TCC will not persist a decision against it. The stored answer
stays "unknown" and the dialog returns forever, whether you clicked Allow or Don't Allow.

## The fix

`scripts/build.sh` signs with a stable identifier (`io.github.verveguy.topic`), which is what lets
one answer stick. It signs a temporary file and moves it into place only once the signature is
verified, so a failed build leaves the last good binary. It also stamps the identifier in at link
time, so the binary can tell whether it was built correctly: `topic up`, `restore`, `cycle` and
`ensure-dispatcher` warn on stderr when it wasn't.

```bash
topic doctor      # what macOS thinks this binary is, and how to fix it
```

**Answer the dialog with Don't Allow.** A denial persists exactly as a grant does, and a grant to
`topic` is inherited by everything it spawns: every session, worker and MCP server. Full Disk
Access on `topic` is Full Disk Access for all of them. If some tool genuinely needs a protected
path it will fail under its own name, and you can grant that app directly.

If you have been living with this already, the signed binary alone won't settle it: the running
tmux server predates it, and its sessions are still parented onto the old one. From a terminal
**outside tmux**:

```bash
./install.sh --all
topic cycle down
tmux kill-server
tccutil reset All io.github.verveguy.topic
topic cycle up
```

[`cycle down`](/claude-topics/running/cycle) puts every topic down and stops every registered daemon
first, so `kill-server` orphans nothing it knows about.

## Sign with a Developer ID if you grant topic anything

An ad-hoc signature is enough for a denial to stick, but macOS records an ad-hoc binary's grants
against its exact hash (`codesign -d -r- ~/.local/bin/topic` shows `designated => cdhash …`), so a
grant silently stops applying after the next rebuild. A Developer ID signature is recorded against
your team ID and identifier instead, and survives rebuilds. Put the identity in `.sign-identity`
(gitignored, per machine) and every build uses it:

```bash
security find-identity -v -p codesigning        # list your identities
echo "Developer ID Application: You (TEAMID)" > .sign-identity
./install.sh
```

`TOPIC_SIGN_IDENTITY=...` overrides it for one build, and `TOPIC_IDENTIFIER` changes the signing
identifier.

## When the repo lives on an external drive

If `~/dev` is on, say, a Thunderbolt SSD, the sessions under `topic` need Full Disk Access to it,
or new file access there can stall behind a permission prompt nobody sees, for example overnight
with the screen locked. After signing with a Developer ID and installing: System Settings →
Privacy & Security → Full Disk Access → **+** → ⌘⇧G → `~/.local/bin/topic`, then restart the tmux
server (`topic cycle down && tmux kill-server && topic cycle up`). That grant covers every session,
so weigh it against the advice above to deny.
