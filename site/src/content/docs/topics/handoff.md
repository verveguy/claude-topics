---
title: Hand off
description: Clear a topic's context without losing the thread.
---

A hand-off retires the current session and starts a fresh one under the same topic name, seeded
with a handoff document the old session wrote.

In practice you say `/handoff`, or `/handoff <focus>`, inside the topic, and the `handoff` skill
drives it. Underneath:

```bash
p=$(topic handoff-path "<name>")   # 1. where the doc goes
# 2. the session writes its handoff doc to $p
topic handoff-swap "<name>" --focus "the thing to pick up first"
```

## Why it detaches

Only the session itself can write its handoff doc, so step 2 has to happen inside it. But step 3
is what *ends* that session, and a command cannot wait for a result it will never live to
receive. So when `handoff-swap` is run from inside its own topic, it returns at once and
detaches: a separate process waits about six seconds, retires the session, and brings the
successor up. It logs to `<profile>/topics/<slug>/swap.log`. Run from anywhere else, it behaves
normally and waits.

## The pieces

- **`--focus`** scopes what the successor starts on. Off-focus work is still recorded as open
  threads in the doc, so nothing is silently dropped: an omitted thread is indistinguishable from
  a finished one.
- **Which doc.** `handoff-swap` uses the newest doc in the topic's `handoffs/` unless you name one:
  `topic handoff-swap "<name>" <doc>`.
- **The successor** gets a new session UUID and the topic's generation goes up by one. Any
  [extra claude arguments](/claude-topics/topics/claude-args) carry over.
- **Nothing is lost.** Handoff docs are kept permanently in
  `<profile>/topics/<slug>/handoffs/`. Retired session UUIDs are archived in the topic's
  `history`, so an old session is still resumable in an emergency with `claude --resume <uuid>`.

## Hand off or fork?

A hand-off keeps one topic and clears its context. If the topic has grown a second subject, it
should be two topics: [fork](/claude-topics/topics/fork) it, then hand the parent off if it should
start clean.
