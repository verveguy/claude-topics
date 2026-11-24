---
title: Modes
description: How a channel addresses its topic, from every message to a team member who joins in.
---

`/topics mode <mode>` sets how the channel addresses its topic. It is per channel and kept in
`links.json`.

| Mode | Who it hears | What wakes it | Everything else |
|---|---|---|---|
| `all` (default) | people who can direct it | every message from them | ignored |
| `mention` | people who can direct it | an @mention of the bot, or a reply in a thread it is in | catch-up |
| `team` | everyone in the channel | an @mention or a reply in its threads, from anyone | catch-up |
| `listen` | everyone in the channel | every message, as it is posted | — |

"People who can direct it" are the allowed users, plus whoever is
[trusted](/claude-topics/slack/trust) in that channel.

## all

Every message from someone who can direct the topic goes to it. The channel is a chat window onto
the session. Messages from anyone else are ignored.

## mention

Only messages that @mention the bot, and replies in threads it is in, wake the topic. A top-level
mention starts a thread the bot is in, and so does a reply the topic posts in a thread.

What else was said reaches it as **catch-up** with the next message that wakes it: up to 30 lines
per channel, grouped by thread, each labelled with its sender. So the topic knows what the
conversation was without being interrupted by every line of it.

## team

As `mention`, but the topic hears everyone in the channel. An @mention or a reply in its threads
wakes it, whoever wrote it; everything else reaches it as catch-up. Each message is labelled with
its sender and whether the topic takes direction from them (`directs="true"`, or "(can direct)" /
"(cannot direct)" in catch-up).

It **acts only for those who can direct it.** With anyone else it is a colleague in conversation:
it answers from what it knows, asks clarifying questions and discusses, but runs no tools for them
beyond replying, and does not start work they ask for; it says that someone who can direct it needs
to ask.

## listen

As `team`, but the topic is sent every message as it is posted, marked `addressed="false"` unless
it @mentions the bot or is in its threads. It is told to stay quiet unless it has something clearly
useful to add (a correction, a fact, a short question) and **never to act on what it overhears**,
whoever wrote it: if a message seems to want something, it offers and waits to be asked.

Each message is a Claude turn, so this costs more. Overheard messages get no 👀. While the topic is
down, what it would have overheard waits as catch-up.

## Starting a down topic

In any mode, a bare `up` from someone who can direct the topic starts it, whether or not it
mentions the bot.
