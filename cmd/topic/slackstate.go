package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The hub's memory of conversations, kept in <Slack home>/state.json so a restart
// loses none of it: catch-up not yet sent, the threads the bot is in, which topic
// sent each notify DM, and messages held for topics that are not listening.

// stateKeep is how long a thread or DM is remembered after it started.
const stateKeep = 30 * 24 * time.Hour

type storedMessage struct {
	Channel string `json:"channel"`
	User    string `json:"user"`
	Text    string `json:"text"`
	TS      string `json:"ts"`
	Thread  string `json:"thread,omitempty"`
}

type hubState struct {
	Unsent     map[string][]storedMessage `json:"unsent,omitempty"`
	BotThreads map[string]bool            `json:"bot_threads,omitempty"`
	DMFrom     map[string]string          `json:"dm_from,omitempty"`
	DMLast     map[string]string          `json:"dm_last,omitempty"`
	DMMayUse   map[string]bool            `json:"dm_may_use,omitempty"`
	Held       map[string][]hubWire       `json:"held,omitempty"`
}

func slackStatePath(home string) string { return filepath.Join(home, "state.json") }

// stale reports whether a "channel/ts" key is older than stateKeep.
func stale(key string, now time.Time) bool {
	i := strings.LastIndex(key, "/")
	sec, err := strconv.ParseFloat(key[i+1:], 64)
	return err == nil && now.Sub(time.Unix(int64(sec), 0)) > stateKeep
}

func (h *hub) loadState() {
	b, err := os.ReadFile(slackStatePath(h.home))
	if err != nil {
		return
	}
	var st hubState
	if err := json.Unmarshal(b, &st); err != nil {
		h.log("ignoring unreadable %s: %v", slackStatePath(h.home), err)
		return
	}
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch, q := range st.Unsent {
		for _, m := range q {
			h.unsent[ch] = append(h.unsent[ch], slackMessage{channel: m.Channel, user: m.User, text: m.Text, ts: m.TS, thread: m.Thread})
		}
	}
	for k := range st.BotThreads {
		if !stale(k, now) {
			h.botThreads[k] = true
		}
	}
	for k, v := range st.DMFrom {
		if !stale(k, now) {
			h.dmFrom[k] = v
		}
	}
	for k, v := range st.DMLast {
		h.dmLast[k] = v
	}
	for k := range st.DMMayUse {
		h.dmMayUse[k] = true
	}
	for k, v := range st.Held {
		h.held[k] = v
	}
}

func (h *hub) saveState() error {
	h.mu.Lock()
	st := hubState{Unsent: map[string][]storedMessage{}, BotThreads: h.botThreads, DMFrom: h.dmFrom,
		DMLast: h.dmLast, DMMayUse: h.dmMayUse, Held: h.held}
	for ch, q := range h.unsent {
		for _, m := range q {
			st.Unsent[ch] = append(st.Unsent[ch], storedMessage{m.channel, m.user, m.text, m.ts, m.thread})
		}
	}
	b, err := json.MarshalIndent(st, "", "  ")
	h.mu.Unlock()
	if err != nil {
		return err
	}
	tmp := slackStatePath(h.home) + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, slackStatePath(h.home))
}

// changed asks for the state to be saved soon; a burst of changes is one write.
func (h *hub) changed() {
	select {
	case h.dirty <- struct{}{}:
	default:
	}
}

func (h *hub) saveLoop() {
	for range h.dirty {
		time.Sleep(200 * time.Millisecond)
		if err := h.saveState(); err != nil {
			h.log("saving %s: %v", slackStatePath(h.home), err)
		}
	}
}
