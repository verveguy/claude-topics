---
name: fork
description: Split a thread out of the current topic into a new topic of its own, seeded with a brief. Use when the user says "fork", "/fork <name>", "split this out", "make that its own topic", "spin that off", or names a side-thread that has grown big enough to deserve its own session. For clearing context on a topic that stays one topic, use the `handoff` skill instead.
---

# Fork a thread into its own topic

A **topic** is a stable name that outlives the Claude session beneath it. Forking
takes one thread out of the topic you are in and gives it its own topic, its own
session, and its own context — seeded with a brief you write.

Invoked as `/fork "<Topic Name>" <what to extract>`.

## If `topic` is missing

These procedures are worthless without the CLI. If a `topic` command fails with
"command not found", run `topic-bootstrap` — it reports what is installed and how to
get the rest. `topic-bootstrap --install` clones and builds it, which installs a
binary and a launchd agent, so **ask the user before running it**.

## Fork or hand off?

- **Fork** — the topic contains two subjects and both are still live. You want two
  sessions. The parent survives.
- **Handoff** (`handoff` skill) — one subject, cluttered context. Same topic name,
  fresh session, nothing splits.

If the thread being extracted is *finished*, neither applies — say so instead of
forking a dead thread into a new topic.

## Procedure

1. **Fix the scope.** The user's extraction prompt (`/fork "Fantasy Images" the
   image-pipeline work`) is the subject boundary. If the prompt is missing or too
   vague to draw a line around, ask — a mis-scoped fork is expensive: the child
   starts confused and the parent keeps the thread anyway.

2. **Get the brief path.** Works before the topic exists:
   ```bash
   p=$(topic fork-path "<new topic>"); echo "$p"
   ```

3. **Write the brief to that exact path** — see below.

4. **Fork.**
   ```bash
   topic fork "<new topic>" [dir] --from "<parent>"
   ```
   `--from` defaults to the topic you are in (`topic whoami`), and the working
   directory defaults to the parent's — pass `dir` only when the child belongs
   somewhere else, such as its own worktree.

5. **Report** the new topic is up, and offer the parent's half (step 6). Unlike a
   handoff, your session survives a fork.

6. **Offer to shed the thread from the parent.** The fork does not remove anything
   from *your* context — you still carry the thread you just handed away. If it was
   substantial, suggest a handoff of the parent so it starts clean:
   ```bash
   topic handoff-swap "<parent>"   # after writing its handoff doc
   ```
   Do not do this unprompted: it terminates the user's current session.

## What goes in a fork brief

A brief is **not** a handoff doc. A handoff is written for a successor continuing
*the same* work with the same history. A brief is written for a session that has
never existed, about a thread it has never seen. Everything implicit must be made
explicit.

- **What this topic is** — one paragraph. Name the parent topic and why this split
  off, so the child knows what it is *not* responsible for.
- **The thread itself** — the actual state of the work being extracted: what exists,
  what works, what is broken, what is committed.
- **Why it deserves its own topic** — the question or goal that motivated the split.
- **Load-bearing specifics** — file paths, branches, PR/issue numbers, commands that
  worked, data locations. The child cannot ask the parent; it only has this file.
- **Decisions already made** — with reasons, so the child does not relitigate them.
- **Explicitly out of scope** — what stays with the parent. Without this the child
  will wander back into the parent's work.
- **First step** — the single most useful thing to do first.

Write specifics over prose. If a detail lives only in your current context, it does
not exist for the child unless you write it down.

## Notes

- Briefs live permanently in `~/.claude/topics/<slug>/briefs/`, separate from
  `handoffs/`. The child records `seededFrom` and `forkedFrom`; the parent records
  the child in `forks`. `topic status` shows both directions.
- `topic fork` refuses if the new topic already has a session — pick another name, or
  pick it up with `topic up`.
- It also refuses on a missing or empty brief. Write the file before running it.
- The child is a full topic immediately: `up`, `down`, `handoff`, and `fork` all work
  on it, and it can be forked again.
