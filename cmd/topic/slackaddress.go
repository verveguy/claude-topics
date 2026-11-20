package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Addressing, catch-up context and mention limits: how a channel with more going on
// than one person and one topic stays usable.

// unsentLimit bounds the catch-up kept per channel.
const unsentLimit = 30

// addressed reports whether a message in a mention-mode channel is for Claude: it
// mentions the bot, or it is in a thread the bot takes part in.
func (h *hub) addressed(m slackMessage) bool {
	if h.botUser != "" && strings.Contains(m.text, "<@"+h.botUser+">") {
		h.mu.Lock()
		h.botThreads[m.channel+"/"+m.ts] = true // a top-level mention starts a thread
		if m.thread != "" {
			h.botThreads[m.channel+"/"+m.thread] = true
		}
		h.mu.Unlock()
		h.changed()
		return true
	}
	if m.thread == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.botThreads[m.channel+"/"+m.thread]
}

func (h *hub) remember(m slackMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	q := append(h.unsent[m.channel], m)
	if len(q) > unsentLimit {
		q = q[len(q)-unsentLimit:]
	}
	h.unsent[m.channel] = q
	h.changed()
}

// withCatchUp prefixes a message with everything said in the channel that Claude was
// not sent, grouped by where it was said (the channel's top level, or each thread),
// and forgets those lines. Claude follows the whole channel, threads included,
// without being woken for every message of it.
func (h *hub) withCatchUp(link slackLink, m slackMessage) string {
	h.mu.Lock()
	before := h.unsent[m.channel]
	delete(h.unsent, m.channel)
	h.mu.Unlock()
	h.changed()
	text := strings.TrimSpace(strings.ReplaceAll(m.text, "<@"+h.botUser+">", ""))
	if len(before) == 0 {
		return text
	}
	// A top-level message that others answered in a thread belongs with its thread.
	parents := map[string]bool{}
	for _, u := range before {
		if u.thread != "" {
			parents[u.thread] = true
		}
	}
	place := func(u slackMessage) string {
		if u.thread != "" {
			return u.thread
		}
		if parents[u.ts] {
			return u.ts
		}
		return ""
	}
	var order []string
	groups := map[string][]slackMessage{}
	for _, u := range before {
		k := place(u)
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], u)
	}
	var b strings.Builder
	b.WriteString("[Said in this channel since you last heard from it:]\n")
	for _, k := range order {
		here := ""
		if k == m.thread {
			here = " (where the message for you was posted)"
		}
		if k == "" {
			fmt.Fprintf(&b, "In the channel%s:\n", here)
		} else {
			fmt.Fprintf(&b, "In the thread thread_ts=%s%s:\n", k, here)
		}
		for _, u := range groups[k] {
			fmt.Fprintf(&b, "  [ts=%s] %s: %s\n", u.ts, h.who(link, u.user), u.text)
		}
	}
	fmt.Fprintf(&b, "[The message for you, from %s:]\n", h.who(link, m.user))
	b.WriteString(text)
	return b.String()
}

// directs reports whether the topic takes direction from a user in this channel: an
// allowed user anywhere, or someone trusted here.
func (h *hub) directs(link slackLink, user string) bool {
	if h.allowed[user] {
		return true
	}
	for _, u := range link.Trusted {
		if u == user {
			return true
		}
	}
	return false
}

// who names a sender for Claude, with whether it may take direction from them.
func (h *hub) who(link slackLink, user string) string {
	if h.directs(link, user) {
		return h.slack.userName(user) + " (can direct)"
	}
	return h.slack.userName(user) + " (cannot direct)"
}

var (
	broadcastMention = regexp.MustCompile(`<!(channel|here|everyone)(\|[^>]*)?>`)
	groupMention     = regexp.MustCompile(`<!subteam\^[A-Z0-9]+(\|([^>]*))?>`)
	userMention      = regexp.MustCompile(`<@([UW][A-Z0-9]+)(\|[^>]*)?>`)
)

// limitMentions keeps what Claude posts from paging people: broadcast and group
// mentions become plain text, and so does a mention of anyone not in the channel. One
// prompt-injected sentence should not be able to notify a whole workspace.
func (h *hub) limitMentions(channel, text string) string {
	text = broadcastMention.ReplaceAllString(text, "@$1")
	text = groupMention.ReplaceAllStringFunc(text, func(s string) string {
		if m := groupMention.FindStringSubmatch(s); m[2] != "" {
			return m[2]
		}
		return "@group"
	})
	if !userMention.MatchString(text) {
		return text
	}
	members := h.channelMembers(channel)
	return userMention.ReplaceAllStringFunc(text, func(s string) string {
		id := userMention.FindStringSubmatch(s)[1]
		if members[id] {
			return s
		}
		return h.slack.userName(id)
	})
}

func (h *hub) channelMembers(channel string) map[string]bool {
	h.mu.Lock()
	c, ok := h.memberSet[channel]
	h.mu.Unlock()
	if ok && time.Since(c.fetched) < 5*time.Minute {
		return c.ids
	}
	ids, err := h.slack.members(channel)
	set := map[string]bool{}
	if err != nil {
		h.log("listing members of %s: %v", channel, err)
		return set // unknown: nobody may be mentioned
	}
	for _, id := range ids {
		set[id] = true
	}
	h.mu.Lock()
	h.memberSet[channel] = memberCache{ids: set, fetched: time.Now()}
	h.mu.Unlock()
	return set
}
