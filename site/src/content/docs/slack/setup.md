---
title: Slack setup
description: Link Slack channels to topics, one channel each, and talk to them there.
---

`topic slack` links Slack channels to topics, one channel each. Messages in the channel reach
the topic's session, and it answers in the channel. A topic can also DM you with `notify`, and your
answer goes back to it.

```bash
topic slack setup                                   # once: Slack home, app manifest, hub daemon
topic slack link "Fantasy Economics" <channel link> # or, in Slack: /topics link Fantasy Economics
topic slack unlink "Fantasy Economics"
topic slack status                                  # the hub, each link, and who is listening
```

## Setting it up

1. **`topic slack setup`**, in the profile that should own the bridge. It creates the Slack home
   (`<profile>/channels/slack/`) with an app manifest and a `.env` to fill in, and registers the
   `Slack Hub` [daemon](/claude-topics/running/daemons), which waits until the tokens are in. One
   profile owns the bridge per machine: setup refuses if another already does.
2. **Create the app.** At [api.slack.com/apps](https://api.slack.com/apps), choose *Create New App
   → From a manifest* and paste `slack-app-manifest.yaml` from the Slack home. Install the app to
   the workspace.
3. **Fill in `.env`:**
   - `SLACK_BOT_TOKEN`: *OAuth & Permissions → Bot User OAuth Token* (`xoxb-…`).
   - `SLACK_APP_TOKEN`: *Basic Information → App-Level Tokens*, with scope `connections:write`
     (`xapp-…`).
   - `SLACK_ALLOWED_USERS`: Slack member IDs, separated by commas or spaces (*profile → ⋯ → Copy
     member ID*). These are the topics' owners.

   The hub picks the tokens up within a minute. It reads `.env` once, so after changing
   `SLACK_ALLOWED_USERS` or a token later, restart it: `topic daemon stop "Slack Hub"`, then
   `topic daemon start "Slack Hub"`.
4. **Invite the bot to a channel.** An allowed user can then run `/topics new <name>` or
   `/topics link <name>` there, or you can run `topic slack link "<topic>" <channel link>`.

Linking adds `server:slack` to the topic's [channels](/claude-topics/topics/claude-args), and
registers the Slack shim in the topic's profile as a user-scope MCP server. A topic that was
already running picks the channel up at its next launch: `/topics restart`.

`topic slack link` works on topics in the current profile (`CLAUDE_CONFIG_DIR`) and needs the
tokens in place; `/topics link` in Slack finds the topic in whichever profile has it. Linking a
channel or topic that is already linked replaces the old link.

## Updating the app

`topic slack setup` is safe to re-run: it rewrites the manifest, keeps `.env`, and keeps the hub's
daemon stopped if you stopped it. When a new version of `topic` needs new Slack scopes or events,
re-run it, then in the app's settings open *App Manifest*, paste the new
`slack-app-manifest.yaml`, save, and reinstall the app to the workspace when Slack asks.

## Next

- [In Slack](/claude-topics/slack/in-slack): the `/topics` commands, held messages, receipts,
  permission prompts and DMs.
- [Modes](/claude-topics/slack/modes): how a channel addresses its topic, from "every message" to
  a team member who joins in.
- [Trust](/claude-topics/slack/trust): who a topic takes direction from, and what that does and
  does not protect.
