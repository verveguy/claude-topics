---
name: handoff
description: Hand off a long-running topic to a fresh Claude session, carrying the salient context across in a handoff document. Use when a session's context has grown cluttered but the work is not finished — the user says "hand off", "hand this off", "clear context but keep the thread", "start fresh on this topic", or names a topic to hand off. Also use when asked to hand off a DIFFERENT topic than the current session.
---

# Hand off a topic

A **topic** is a stable name ("Fantasy Economics") that outlives the Claude session
beneath it. Handing off retires the current session and starts a fresh one under the
same topic name, seeded with a handoff document. Context is cleared; the salient
details survive.

Backed by the `topic` CLI (`~/.local/bin/topic`). Run `topic --help` for the full
surface.

## If `topic` is missing

These procedures are worthless without the CLI. If a `topic` command fails with
"command not found", run `topic-bootstrap` — it reports what is installed and how to
get the rest. `topic-bootstrap --install` clones and builds it, which installs a
binary and a launchd agent, so **ask the user before running it**.

## Scoping: `/handoff <focus>`

The user will often name a focus — `/handoff the openai investigation`, or "hand this
off, just the streaming work". Treat that as **what the successor should pick up
first**, and let it drive both the document and the seed prompt.

A focus changes emphasis. It does **not** license silently dropping the rest:

- **In focus** — full detail. Current state, decisions, exact paths, next step.
- **Out of focus but still open** — a short "Other open threads" list, one line each,
  enough that the successor knows they exist and can ask. Never omit an unfinished
  thread just because it is off-focus: a dropped thread is indistinguishable from a
  finished one.
- **Genuinely finished** — say so explicitly, so the successor does not reopen it.

Pass the focus through to the swap so the new session starts in the right place:

```bash
topic handoff-swap "<topic>" --focus "the OpenAI streaming investigation"
```

With no focus given, write a general handoff covering all live threads evenly. If the
session covered several substantial unrelated threads and the user has not said which
matters, ask before writing — a mis-scoped handoff is expensive to recover from.

If the session's context is cluttered because it is carrying **two subjects**, and
both are still live, that is a fork, not a handoff — see the `fork` skill. A handoff
keeps one topic and refreshes its session; a fork splits one topic into two.

## Which case is this?

**Case A — hand off THIS session.** You are the session being handed off. You write
the handoff document yourself, then the swap happens.

**Case B — hand off ANOTHER topic.** You are a coordinator (often the Dispatcher).
You ask the target session to write its own handoff doc, wait for it, then swap.

## Case A: handing off the session you are in

1. Confirm the topic name. If the user did not say it, ask — do not guess. If this
   session is not a registered topic, `topic list` will not show it; see
   "Not a topic yet" below.

2. Get the destination path:
   ```bash
   p=$(topic handoff-path "<topic>"); echo "$p"
   ```

3. Write the handoff document to that exact path, following the structure below.

4. Perform the swap:
   ```bash
   topic handoff-swap "<topic>"
   ```
   Run from inside the topic being swapped, this **returns immediately** and schedules
   the work: a detached process retires your session a few seconds later, archives its
   UUID in the topic's history, and starts a fresh session under the same name seeded
   with the doc. The command cannot wait for that, because it is what ends you.

5. **Say your goodbye in the same turn.** You have a few seconds. Tell the user the
   swap is scheduled and that a fresh session will come up under the same name.

## Case B: handing off another topic

1. `topic list` to confirm the topic is up.

2. `p=$(topic handoff-path "<topic>")`

3. Call `ListAgents`, then `SendMessage` the target session (a `[ref]` suffix is
   usually required). Ask it to write its handoff doc to `$p`, following the
   structure below, and to reply when done.

4. Wait for the file to be non-empty before continuing:
   ```bash
   for i in $(seq 1 60); do [[ -s "$p" ]] && break; sleep 2; done; ls -l "$p"
   ```

5. `topic handoff-swap "<topic>"`

6. Report the new generation number and that the topic is up.

## What goes in a handoff document

Write for a successor with **no memory of this conversation**. Favour specifics —
paths, issue numbers, branch names, commands — over summary prose. Include:

- **What this topic is about** — one paragraph of orientation.
- **Current state** — what is done, what is in flight, what is committed vs not.
- **Decisions made** — and *why*, so the successor does not relitigate them.
- **Open threads** — unresolved questions, things waiting on the user or on CI.
- **Next step** — the single most useful thing to do next.
- **Load-bearing details** — exact file paths, issue/PR numbers, branch names,
  commands that worked, gotchas discovered.

Explicitly note anything the successor would otherwise get wrong: a failed approach
already ruled out, a misleading test, a stale document.

Do **not** pad it. A handoff doc is a working brief, not a report.

## Not a topic yet

If the work is not yet a registered topic, register it first, then hand off:
```bash
topic up "<name>" [dir]     # creates the topic
```
For a session already running outside topic management, see the `topic-manage` skill
(adoption) — a live session must exit before it can be claimed, or two processes will
resume the same session UUID and corrupt the transcript.

## Notes

- Handoff docs are kept permanently in `~/.claude/topics/<slug>/handoffs/`. Reading an
  older one is a legitimate way to recover context that a handoff dropped.
- `topic handoff-swap` refuses to run if the handoff doc is missing or empty.
- Retired session UUIDs are archived in the topic's `history`, so an old session is
  still resumable in an emergency.
