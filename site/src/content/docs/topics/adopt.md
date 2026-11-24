---
title: Adopting a session
description: Bring a session topic did not launch under management, live or merely resumable.
---

A session started outside `topic`, with a plain `claude`, can be brought under management. Every
live session publishes `<profile>/sessions/<pid>.json`, the authoritative `name` → `sessionId` →
`cwd` mapping, so adoption normally needs no probe and no round trip:

```bash
topic adopt "<name>"                 # matches a live session whose name is <name>
topic adopt "<name>" --pid 61222     # or point straight at one
topic adopt "<name>" --dir ~/dev/x   # or narrow by working directory
```

Candidates are narrowed by `--pid`, then `--dir`, then by a name equal to the topic name. If that
doesn't leave exactly one, `topic` lists the live sessions with the `--pid` needed to pick one; it
never guesses. Records linger after a session exits, so liveness is re-checked against the pid.

## A session that has exited

Anything `claude --resume` lists can be adopted too. Its name is in its own transcript, as
`custom-title` records, which is where `--resume` reads it from. Same command: `topic` falls
through to this when nothing live matches.

```bash
topic adopt "Fantasy UX"                      # a resumable session, not running
topic adopt "<name>" --title "<resume name>"  # topic name differs from the title
```

Sessions get **renamed**, and every rename stamps a new title, so only the *last* one counts. If
several sessions share the title, `topic` lists them newest first with the `--session-id` to pick
one. There is no process to claim, so the topic lands in the ordinary `down` state and `topic up`
simply resumes it. The scan reads every transcript in parallel (about a third of a second over
800MB), and is still the fallback rather than the first thing tried.

## By UUID, or by probe

`--session-id <uuid>` takes a UUID you already know: a session adopting *itself*, or your pick
from an ambiguous list. If that session is running, its pid is recorded, exactly as for a live
record.

`--probe` is for a session that is running but *unnamed*, with nothing to match on. A running
session doesn't hold its transcript open, so you send it a unique token and `topic` finds which
transcript the token landed in. Exclude your own session, whose transcript has the token too
because you sent it:

```bash
topic adopt "<name>" --probe <token> --exclude <your-own-session-uuid>
```

`adopt` is non-destructive: it only records the UUID, directory and pid. `--dry-run` reports what
it would adopt without registering anything, which is the safe way to answer "is this session
already a topic, and which one?"

## Then claim it

An adopted session is still running outside tmux, and `topic list` shows it as **ADOPTED**.
Starting it under `topic` would resume the same session UUID in a *second* process: two writers
on one transcript, which corrupts it. So exit the original first (`/exit` in its window).

```bash
topic up "<name>"           # pid on record: refused while it is alive, fine once it has gone
topic up "<name>" --claim   # adopted by --probe: you assert the original has exited
```

Adoption by live record or `--session-id` stores the pid, so `topic up` can *see* the original:
it refuses while that process is alive (`--claim` will not override it) and needs no assertion
once it has gone. A `--probe` match has no pid to check, so `up` refuses until you pass `--claim`.

`topic down` cannot stop an adopted session either; it says so. A topic adopted from a resumable,
exited session needs no claim at all: it is already down.

Don't adopt Fabrik workers or other automation, only your own interactive sessions.
