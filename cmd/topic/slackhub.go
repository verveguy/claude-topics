package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// hubWire is one line of the hub <-> shim protocol: newline-delimited JSON over the
// hub's unix socket, which only this user can open (0600, in a 0700 directory).
//
//	shim -> hub   hello {topic, configDir}       call {id, tool, args}
//	              permission_request {requestId, toolName, description, inputPreview}
//	              status                          (from `topic slack status`)
//	hub -> shim   welcome {channel}               "" when the topic has no channel
//	              inbound {content, meta}         result {id, text, isError}
//	              permission {requestId, behavior}
type hubWire struct {
	Type         string            `json:"type"`
	ID           int               `json:"id,omitempty"`
	Topic        string            `json:"topic,omitempty"`
	ConfigDir    string            `json:"configDir,omitempty"`
	Channel      string            `json:"channel,omitempty"`
	Tool         string            `json:"tool,omitempty"`
	Args         map[string]any    `json:"args,omitempty"`
	Text         string            `json:"text,omitempty"`
	IsError      bool              `json:"isError,omitempty"`
	Content      string            `json:"content,omitempty"`
	Meta         map[string]string `json:"meta,omitempty"`
	RequestID    string            `json:"requestId,omitempty"`
	ToolName     string            `json:"toolName,omitempty"`
	Description  string            `json:"description,omitempty"`
	InputPreview string            `json:"inputPreview,omitempty"`
	Behavior     string            `json:"behavior,omitempty"`
	Listening    []string          `json:"listening,omitempty"`
}

// slackAPI is the part of Slack the hub uses, so tests can stand in for it.
type slackAPI interface {
	post(channel, thread, text string, blocks []slack.Block) (string, error)
	update(channel, ts, text string, blocks []slack.Block) error
	react(channel, ts, name string) error
	ephemeral(channel, user, text string) error
	history(channel, thread string, limit int) ([]slack.Message, error)
	userName(id string) string
	canRead(channel string) error
	members(channel string) ([]string, error)
	dm(user, text string) (channel, ts string, err error)
}

type realSlack struct {
	api   *slack.Client
	mu    sync.Mutex
	names map[string]string
}

func newRealSlack(botToken string, opts ...slack.Option) *realSlack {
	return &realSlack{api: slack.New(botToken, opts...), names: map[string]string{}}
}

func (r *realSlack) post(channel, thread, text string, blocks []slack.Block) (string, error) {
	opts := []slack.MsgOption{slack.MsgOptionText(text, false)}
	if len(blocks) > 0 {
		opts = append(opts, slack.MsgOptionBlocks(blocks...))
	}
	if thread != "" {
		opts = append(opts, slack.MsgOptionTS(thread))
	}
	_, ts, err := r.api.PostMessage(channel, opts...)
	return ts, err
}

func (r *realSlack) update(channel, ts, text string, blocks []slack.Block) error {
	opts := []slack.MsgOption{slack.MsgOptionText(text, false)}
	if blocks != nil {
		opts = append(opts, slack.MsgOptionBlocks(blocks...))
	}
	_, _, _, err := r.api.UpdateMessage(channel, ts, opts...)
	return err
}

func (r *realSlack) react(channel, ts, name string) error {
	err := r.api.AddReaction(name, slack.NewRefToMessage(channel, ts))
	if err != nil && strings.Contains(err.Error(), "already_reacted") {
		return nil
	}
	return err
}

func (r *realSlack) ephemeral(channel, user, text string) error {
	_, err := r.api.PostEphemeral(channel, user, slack.MsgOptionText(text, false))
	return err
}

func (r *realSlack) history(channel, thread string, limit int) ([]slack.Message, error) {
	if thread != "" {
		msgs, _, _, err := r.api.GetConversationReplies(&slack.GetConversationRepliesParameters{ChannelID: channel, Timestamp: thread, Limit: limit})
		return msgs, err
	}
	resp, err := r.api.GetConversationHistory(&slack.GetConversationHistoryParameters{ChannelID: channel, Limit: limit})
	if err != nil {
		return nil, err
	}
	msgs := resp.Messages
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 { // newest first -> oldest first
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

func (r *realSlack) userName(id string) string {
	r.mu.Lock()
	n, ok := r.names[id]
	r.mu.Unlock()
	if ok {
		return n
	}
	n = id
	if u, err := r.api.GetUserInfo(id); err == nil {
		for _, c := range []string{u.Profile.DisplayName, u.RealName, u.Name} {
			if c != "" {
				n = c
				break
			}
		}
	}
	r.mu.Lock()
	r.names[id] = n
	r.mu.Unlock()
	return n
}

func (r *realSlack) members(channel string) ([]string, error) {
	var all []string
	cursor := ""
	for {
		ids, next, err := r.api.GetUsersInConversation(&slack.GetUsersInConversationParameters{ChannelID: channel, Cursor: cursor, Limit: 200})
		if err != nil {
			return nil, err
		}
		all = append(all, ids...)
		if next == "" {
			return all, nil
		}
		cursor = next
	}
}

func (r *realSlack) dm(user, text string) (string, string, error) {
	ch, _, _, err := r.api.OpenConversation(&slack.OpenConversationParameters{Users: []string{user}})
	if err != nil {
		if strings.Contains(err.Error(), "missing_scope") {
			return "", "", errors.New("the Slack app lacks the im:write scope: update its manifest and reinstall it")
		}
		return "", "", err
	}
	_, ts, err := r.api.PostMessage(ch.ID, slack.MsgOptionText(text, false))
	return ch.ID, ts, err
}

// canRead checks the bot can see a channel before it is linked: the usual miss is a
// private channel nobody invited it to, which Slack reports as not found.
func (r *realSlack) canRead(channel string) error {
	_, err := r.api.GetConversationHistory(&slack.GetConversationHistoryParameters{ChannelID: channel, Limit: 1})
	if err == nil {
		return nil
	}
	if s := err.Error(); strings.Contains(s, "channel_not_found") || strings.Contains(s, "not_in_channel") {
		who := "the bot"
		if a, aerr := r.api.AuthTest(); aerr == nil {
			who = "@" + a.User
		}
		return fmt.Errorf("the bot cannot see %s: invite %s to the channel (/invite %s) and check the ID", channel, who, who)
	}
	return fmt.Errorf("checking %s: %w", channel, err)
}

// ---- the hub --------------------------------------------------------------------------

type shimConn struct {
	key  string
	conn net.Conn
	mu   sync.Mutex
	enc  *json.Encoder
}

func (s *shimConn) send(w hubWire) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return s.enc.Encode(w)
}

type pendingPermission struct {
	shim        *shimConn
	channel, ts string
	text        string
}

type hub struct {
	home    string
	allowed map[string]bool
	slack   slackAPI
	log     func(format string, a ...any)

	botUser string

	// topicUp, startTopic, runTopic and profiles are isUp, `topic up`, any topic
	// command run in a profile, and listProfiles: swappable for tests.
	topicUp    func(l slackLink) bool
	startTopic func(l slackLink) error
	runTopic   func(configDir string, args ...string) (string, error)
	profiles   func() []string
	interrupt  func(l slackLink) error // Escape into the topic's pane

	mu        sync.Mutex
	links     []slackLink
	linksMod  time.Time
	shims     map[string]*shimConn          // by topic key
	pending   map[string]*pendingPermission // by request id
	held      map[string][]hubWire          // by channel: messages for a topic not listening
	noticed   map[string]time.Time          // by channel: when we last said it was not listening
	starting  map[string]bool               // by topic key
	slackText string                        // connection state, for status

	// For channels in mention mode: what was said that Claude was not sent, by
	// channel, and the threads it takes part in (channel/thread_ts).
	unsent     map[string][]slackMessage
	botThreads map[string]bool
	memberSet  map[string]memberCache

	// Direct messages: notify's DM (channel/ts) and the topic that sent it, the topic
	// that last notified in each DM channel, and the DM channels each topic may answer
	// in ("key|channel").
	dmFrom   map[string]string
	dmLast   map[string]string
	dmMayUse map[string]bool

	dirty chan struct{} // see changed
}

type memberCache struct {
	ids     map[string]bool
	fetched time.Time
}

// heldLimit bounds the messages kept for a topic that is not listening.
const heldLimit = 20

func newHub(home string, cfg slackConfig, api slackAPI) *hub {
	h := &hub{
		home: home, allowed: cfg.allowed, slack: api,
		log:   func(f string, a ...any) { fmt.Fprintf(os.Stderr, time.Now().Format("15:04:05 ")+f+"\n", a...) },
		shims: map[string]*shimConn{}, pending: map[string]*pendingPermission{},
		held: map[string][]hubWire{}, noticed: map[string]time.Time{}, starting: map[string]bool{},
		slackText: "connecting",
		unsent:    map[string][]slackMessage{}, botThreads: map[string]bool{}, memberSet: map[string]memberCache{},
		dmFrom: map[string]string{}, dmLast: map[string]string{}, dmMayUse: map[string]bool{},
		dirty: make(chan struct{}, 1),
	}
	h.topicUp = func(l slackLink) bool { return isUp(profileTag(l.ConfigDir), l.Topic) }
	h.runTopic = func(configDir string, args ...string) (string, error) {
		self, err := os.Executable()
		if err != nil {
			return "", err
		}
		cmd := exec.Command(self, args...)
		cmd.Env = envForProfile(configDir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return string(out), fmt.Errorf("%v: %s", err, lastLines(string(out), 6))
		}
		return string(out), nil
	}
	h.startTopic = func(l slackLink) error { _, err := h.runTopic(l.ConfigDir, "up", l.Topic); return err }
	h.profiles = listProfiles
	h.interrupt = func(l slackLink) error {
		return tmuxCmd("send-keys", "-t", exactTarget(tmuxName(profileTag(l.ConfigDir), l.Topic)), "Escape").Run()
	}
	h.reloadLinks()
	h.loadState()
	go h.saveLoop()
	return h
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// reloadLinks re-reads links.json when it changed, and tells each connected shim if its
// channel did: `topic slack link` edits the file while the hub runs.
func (h *hub) reloadLinks() {
	fi, err := os.Stat(slackLinksPath(h.home))
	h.mu.Lock()
	if err == nil && fi.ModTime().Equal(h.linksMod) || err != nil && h.linksMod.IsZero() && h.links == nil {
		h.mu.Unlock()
		return
	}
	h.mu.Unlock()
	links, lerr := loadLinks(h.home)
	if lerr != nil {
		h.log("links: %v", lerr)
		return
	}
	h.mu.Lock()
	before := map[string]string{}
	for _, l := range h.links {
		before[l.key()] = l.Channel
	}
	h.links = links
	h.linksMod = time.Time{}
	if err == nil {
		h.linksMod = fi.ModTime()
	}
	after := map[string]string{}
	for _, l := range links {
		after[l.key()] = l.Channel
	}
	var changed []*shimConn
	for k, s := range h.shims {
		if before[k] != after[k] {
			changed = append(changed, s)
		}
	}
	h.mu.Unlock()
	for _, s := range changed {
		ch := after[s.key]
		_ = s.send(hubWire{Type: "welcome", Channel: ch})
		if ch != "" {
			h.deliverHeld(ch, s)
		}
	}
}

func (h *hub) linkForChannel(channel string) (slackLink, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, l := range h.links {
		if l.Channel == channel {
			return l, true
		}
	}
	return slackLink{}, false
}

func (h *hub) channelForKey(key string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, l := range h.links {
		if l.key() == key {
			return l.Channel
		}
	}
	return ""
}

// ---- from Slack -----------------------------------------------------------------------

type slackMessage struct {
	channel, user, text, ts, thread string
}

// permissionReply is the text form of an answer to a permission prompt: "yes abcde".
// Request IDs are five lowercase letters other than l.
var permissionReply = regexp.MustCompile(`(?i)^\s*(y|yes|n|no)\s+([a-km-z]{5})\s*$`)

func (h *hub) onMessage(m slackMessage) {
	if strings.HasPrefix(m.channel, "D") {
		if h.allowed[m.user] {
			h.onDM(m)
		}
		return
	}
	link, ok := h.linkForChannel(m.channel)
	if !ok {
		if h.allowed[m.user] {
			h.promptUnlinked(m.channel, m.thread, false)
		}
		return
	}
	// Outside team mode the topic hears only the people it takes direction from.
	directs := h.directs(link, m.user)
	if !directs && link.Mode != modeTeam && link.Mode != modeListen {
		return
	}
	if r := permissionReply.FindStringSubmatch(m.text); r != nil && h.allowed[m.user] {
		allow := strings.HasPrefix(strings.ToLower(r[1]), "y")
		if h.answerPermission(strings.ToLower(r[2]), allow, m.user, m.channel) {
			_ = h.slack.react(m.channel, m.ts, map[bool]string{true: "white_check_mark", false: "x"}[allow])
			return
		}
	}

	h.mu.Lock()
	listening := h.shims[link.key()] != nil
	h.mu.Unlock()
	content := m.text
	// In mention mode a bare `up` still starts a topic that is not listening; to a
	// running one it is just chatter.
	isUp := directs && strings.EqualFold(strings.TrimSpace(m.text), "up")
	addressed := true
	if link.Mode != "" && !(isUp && !listening) {
		addressed = h.addressed(m)
		switch {
		case addressed:
			content = h.withCatchUp(link, m)
		case link.Mode == modeListen && listening:
			// Listen mode passes everything on as it is said, quietly: no receipt.
		default:
			h.remember(m)
			return
		}
	}

	meta := map[string]string{"channel": m.channel, "ts": m.ts, "user": h.slack.userName(m.user), "user_id": m.user,
		"directs": strconv.FormatBool(directs), "addressed": strconv.FormatBool(addressed)}
	if m.thread != "" {
		meta["thread_ts"] = m.thread
	}
	w := hubWire{Type: "inbound", Content: content, Meta: meta}

	h.mu.Lock()
	s := h.shims[link.key()]
	h.mu.Unlock()
	if s != nil && s.send(w) == nil {
		if addressed {
			_ = h.slack.react(m.channel, m.ts, "eyes")
		}
		return
	}
	if !addressed { // overheard while the topic went away: keep it as catch-up
		h.remember(m)
		return
	}

	running := h.topicUp(link)
	if isUp {
		h.bringUp(link, m, running)
		return
	}

	h.mu.Lock()
	q := append(h.held[m.channel], w)
	if len(q) > heldLimit {
		q = q[len(q)-heldLimit:]
	}
	h.held[m.channel] = q
	recent := time.Since(h.noticed[m.channel]) < 10*time.Minute
	h.noticed[m.channel] = time.Now()
	h.mu.Unlock()
	h.changed()
	_ = h.slack.react(m.channel, m.ts, "inbox_tray")
	if recent {
		return
	}
	var notice string
	if running {
		notice = fmt.Sprintf("*%s* is running but not listening to Slack: it was started before this channel was linked. "+
			"It picks the channel up at its next launch (`topic down %q && topic up %q`). I'm holding your message until then.",
			link.Topic, link.Topic, link.Topic)
	} else if directs {
		notice = fmt.Sprintf("*%s* is down. Reply `up` (or `/topics up`) to start it, and I'll pass your message on once it's running.", link.Topic)
	} else {
		notice = fmt.Sprintf("*%s* is down. I'll pass your message on when it is next started.", link.Topic)
	}
	if _, err := h.slack.post(m.channel, m.thread, notice, nil); err != nil {
		h.log("posting to %s: %v", m.channel, err)
	}
}

// onDM passes a direct message to the topic it answers: the one whose notify it is a
// thread reply to, or else the one that last notified in this DM.
func (h *hub) onDM(m slackMessage) {
	h.mu.Lock()
	key := h.dmLast[m.channel]
	if k, ok := h.dmFrom[m.channel+"/"+m.thread]; ok && m.thread != "" {
		key = k
	}
	s := h.shims[key]
	h.mu.Unlock()
	if key == "" {
		_, _ = h.slack.post(m.channel, m.thread, "I pass a DM on to the topic that last notified you here, and none has yet. "+
			"Talk to a topic in its channel.", nil)
		return
	}
	channel := h.channelForKey(key)
	link, _ := h.linkForChannel(channel)
	if s == nil {
		_, _ = h.slack.post(m.channel, m.thread, fmt.Sprintf("*%s* is not listening right now. Try it in <#%s>.", link.Topic, channel), nil)
		return
	}
	meta := map[string]string{"channel": m.channel, "ts": m.ts, "user": h.slack.userName(m.user), "user_id": m.user, "dm": "true"}
	if m.thread != "" {
		meta["thread_ts"] = m.thread
	}
	if s.send(hubWire{Type: "inbound", Content: m.text, Meta: meta}) == nil {
		_ = h.slack.react(m.channel, m.ts, "eyes")
	}
}

func (h *hub) bringUp(link slackLink, m slackMessage, running bool) {
	if running {
		_, _ = h.slack.post(m.channel, m.thread, fmt.Sprintf("*%s* is already running, but not listening to Slack. "+
			"It needs a relaunch to pick the channel up: `topic down %q && topic up %q`.", link.Topic, link.Topic, link.Topic), nil)
		return
	}
	h.mu.Lock()
	if h.starting[link.key()] {
		h.mu.Unlock()
		_ = h.slack.react(m.channel, m.ts, "hourglass_flowing_sand")
		return
	}
	h.starting[link.key()] = true
	h.mu.Unlock()
	_ = h.slack.react(m.channel, m.ts, "hourglass_flowing_sand")
	h.log("starting %q for %s", link.Topic, m.user)
	go func() {
		err := h.startTopic(link)
		h.mu.Lock()
		delete(h.starting, link.key())
		h.mu.Unlock()
		if err != nil {
			h.log("starting %q: %v", link.Topic, err)
			_, _ = h.slack.post(m.channel, m.thread, fmt.Sprintf("Could not start *%s*:\n```%s```", link.Topic, err), nil)
			return
		}
		_ = h.slack.react(m.channel, m.ts, "white_check_mark")
	}()
}

func (h *hub) onInteraction(cb slack.InteractionCallback) {
	if cb.Type != slack.InteractionTypeBlockActions {
		return
	}
	for _, a := range cb.ActionCallback.BlockActions {
		if a.ActionID != "perm_allow" && a.ActionID != "perm_deny" {
			continue
		}
		if !h.allowed[cb.User.ID] {
			_ = h.slack.ephemeral(cb.Channel.ID, cb.User.ID, "Only allowed users can answer permission prompts.")
			continue
		}
		h.answerPermission(a.Value, a.ActionID == "perm_allow", cb.User.ID, cb.Channel.ID)
	}
}

// answerPermission passes an answer to the session that asked, if the prompt is one we
// posted in this channel; it reports whether it was.
func (h *hub) answerPermission(id string, allow bool, user, channel string) bool {
	h.mu.Lock()
	p := h.pending[id]
	if p == nil || p.channel != channel {
		h.mu.Unlock()
		return false
	}
	delete(h.pending, id)
	h.mu.Unlock()
	behavior, outcome := "deny", fmt.Sprintf(":x: Denied by <@%s>", user)
	if allow {
		behavior, outcome = "allow", fmt.Sprintf(":white_check_mark: Allowed by <@%s>", user)
	}
	_ = p.shim.send(hubWire{Type: "permission", RequestID: id, Behavior: behavior})
	// Replace the buttons with the outcome, so the prompt cannot be answered twice.
	_ = h.slack.update(channel, p.ts, p.text+"\n"+outcome, []slack.Block{
		slack.NewSectionBlock(slack.NewTextBlockObject(slack.MarkdownType, p.text, false, false), nil, nil),
		slack.NewContextBlock("", slack.NewTextBlockObject(slack.MarkdownType, outcome, false, false)),
	})
	return true
}

// ---- from the shims -------------------------------------------------------------------

func (h *hub) serveShim(conn net.Conn) {
	defer conn.Close()
	dec := json.NewDecoder(bufio.NewReader(conn))
	var hello hubWire
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if err := dec.Decode(&hello); err != nil {
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	s := &shimConn{conn: conn, enc: json.NewEncoder(conn)}
	if hello.Type == "status" {
		h.mu.Lock()
		w := hubWire{Type: "status", Text: h.slackText}
		for k := range h.shims {
			w.Listening = append(w.Listening, k)
		}
		h.mu.Unlock()
		sort.Strings(w.Listening)
		_ = s.send(w)
		return
	}
	if hello.Type != "hello" || hello.Topic == "" {
		return
	}
	s.key = topicKey(hello.ConfigDir, hello.Topic)
	h.mu.Lock()
	old := h.shims[s.key]
	h.shims[s.key] = s
	h.mu.Unlock()
	if old != nil {
		old.conn.Close() // a relaunch: the newer session is the one to talk to
	}
	h.log("%q is listening", hello.Topic)
	channel := h.channelForKey(s.key)
	_ = s.send(hubWire{Type: "welcome", Channel: channel})
	if channel != "" {
		h.deliverHeld(channel, s)
	}

	defer func() {
		h.mu.Lock()
		if h.shims[s.key] == s {
			delete(h.shims, s.key)
		}
		for id, p := range h.pending {
			if p.shim == s {
				delete(h.pending, id)
			}
		}
		h.mu.Unlock()
		h.log("%q stopped listening", hello.Topic)
	}()
	for {
		var w hubWire
		if err := dec.Decode(&w); err != nil {
			return
		}
		switch w.Type {
		case "call":
			go func() {
				text, err := h.runTool(s.key, w.Tool, w.Args)
				r := hubWire{Type: "result", ID: w.ID, Text: text}
				if err != nil {
					r.Text, r.IsError = err.Error(), true
				}
				_ = s.send(r)
			}()
		case "permission_request":
			h.relayPermission(s, w)
		}
	}
}

func (h *hub) deliverHeld(channel string, s *shimConn) {
	h.mu.Lock()
	q := h.held[channel]
	delete(h.held, channel)
	delete(h.noticed, channel)
	h.mu.Unlock()
	if len(q) > 0 {
		h.changed()
	}
	for _, w := range q {
		w.Meta["held"] = "true"
		if s.send(w) == nil {
			_ = h.slack.react(channel, w.Meta["ts"], "eyes")
		}
	}
}

func (h *hub) relayPermission(s *shimConn, w hubWire) {
	channel := h.channelForKey(s.key)
	if channel == "" || w.RequestID == "" {
		return
	}
	preview := w.InputPreview
	if len(preview) > 2500 {
		preview = preview[:2500] + "…"
	}
	text := fmt.Sprintf(":lock: *Claude wants to run %s*\n%s", w.ToolName, w.Description)
	md := func(t string) *slack.TextBlockObject {
		return slack.NewTextBlockObject(slack.MarkdownType, t, false, false)
	}
	blocks := []slack.Block{slack.NewSectionBlock(md(text), nil, nil)}
	if preview != "" {
		blocks = append(blocks, slack.NewSectionBlock(md("```"+preview+"```"), nil, nil))
	}
	allow := slack.NewButtonBlockElement("perm_allow", w.RequestID, slack.NewTextBlockObject(slack.PlainTextType, "Allow", false, false))
	allow.Style = slack.StylePrimary
	deny := slack.NewButtonBlockElement("perm_deny", w.RequestID, slack.NewTextBlockObject(slack.PlainTextType, "Deny", false, false))
	deny.Style = slack.StyleDanger
	blocks = append(blocks,
		slack.NewActionBlock("permission", allow, deny),
		slack.NewContextBlock("", md(fmt.Sprintf("or reply `yes %s` / `no %s`", w.RequestID, w.RequestID))))
	ts, err := h.slack.post(channel, "", text+fmt.Sprintf("\nReply `yes %s` or `no %s`.", w.RequestID, w.RequestID), blocks)
	if err != nil {
		h.log("posting permission prompt: %v", err)
		return
	}
	h.mu.Lock()
	h.pending[w.RequestID] = &pendingPermission{shim: s, channel: channel, ts: ts, text: text}
	h.mu.Unlock()
}

// slackChunk is where long replies are split: Slack takes far more, but reads badly.
const slackChunk = 3500

func splitForSlack(text string) []string {
	var out []string
	for len(text) > slackChunk {
		cut := strings.LastIndex(text[:slackChunk], "\n")
		if cut < slackChunk/2 {
			cut = slackChunk
		}
		out = append(out, text[:cut])
		text = strings.TrimPrefix(text[cut:], "\n")
	}
	if text != "" {
		out = append(out, text)
	}
	return out
}

// runTool runs a shim's tool call against the topic's own channel, and no other: a tool
// that could post anywhere would be one prompt injection away from doing so.
func (h *hub) runTool(key, tool string, args map[string]any) (string, error) {
	channel := h.channelForKey(key)
	if channel == "" {
		return "", errors.New("this topic is not linked to a Slack channel (topic slack link)")
	}
	str := func(k string) string { s, _ := args[k].(string); return s }
	home, _ := h.linkForChannel(channel)
	// A topic may answer in a DM it notified in, and nowhere else but its channel.
	if c := str("channel"); c != "" && c != channel {
		h.mu.Lock()
		ok := h.dmMayUse[key+"|"+c]
		h.mu.Unlock()
		if !ok {
			return "", fmt.Errorf("this topic can post only in its own channel or a DM it sent with notify, not %s", c)
		}
		channel = c
	}
	switch tool {
	case "reply":
		text := str("text")
		if text == "" {
			return "", errors.New("text is required")
		}
		text = h.limitMentions(channel, text)
		if t := str("thread_ts"); t != "" {
			h.mu.Lock()
			h.botThreads[channel+"/"+t] = true
			h.mu.Unlock()
			h.changed()
		}
		var posted []string
		for _, part := range splitForSlack(text) {
			ts, err := h.slack.post(channel, str("thread_ts"), part, nil)
			if err != nil {
				return "", err
			}
			posted = append(posted, ts)
		}
		return "posted ts=" + strings.Join(posted, ","), nil
	case "edit_message":
		if str("ts") == "" || str("text") == "" {
			return "", errors.New("ts and text are required")
		}
		return "edited", h.slack.update(channel, str("ts"), h.limitMentions(channel, str("text")), nil)
	case "react":
		name := strings.Trim(str("name"), ":")
		if str("ts") == "" || name == "" {
			return "", errors.New("ts and name are required")
		}
		return "reacted", h.slack.react(channel, str("ts"), name)
	case "channel_members":
		ids, err := h.slack.members(channel)
		if err != nil {
			return "", err
		}
		var lines []string
		for _, id := range ids {
			if id == h.botUser {
				continue
			}
			lines = append(lines, fmt.Sprintf("%s  <@%s>", h.slack.userName(id), id))
		}
		return strings.Join(lines, "\n"), nil
	case "notify":
		text := str("text")
		if text == "" {
			return "", errors.New("text is required")
		}
		link, _ := h.linkForChannel(channel)
		msg := fmt.Sprintf("From *%s* (<#%s>): %s", link.Topic, channel, text)
		var sent []string
		for u := range h.allowed {
			ch, ts, err := h.slack.dm(u, msg)
			if err != nil {
				return "", err
			}
			h.mu.Lock()
			h.dmFrom[ch+"/"+ts] = key
			h.dmLast[ch] = key
			h.dmMayUse[key+"|"+ch] = true
			h.mu.Unlock()
			h.changed()
			sent = append(sent, u)
		}
		return fmt.Sprintf("sent to %d allowed user(s); their answers come back as DMs (dm=\"true\")", len(sent)), nil
	case "read_history":
		limit := 20
		if f, ok := args["limit"].(float64); ok && f >= 1 {
			limit = min(int(f), 100)
		}
		msgs, err := h.slack.history(channel, str("thread_ts"), limit)
		if err != nil {
			return "", err
		}
		var lines []string
		for _, m := range msgs {
			who := "bot"
			if m.User != "" {
				who = h.slack.userName(m.User)
			}
			extra := ""
			if m.ReplyCount > 0 {
				extra = fmt.Sprintf(" replies=%d", m.ReplyCount)
			}
			if m.User != "" && !strings.HasPrefix(channel, "D") {
				who = h.who(home, m.User)
			}
			lines = append(lines, fmt.Sprintf("[ts=%s%s] %s: %s", m.Timestamp, extra, who, m.Text))
		}
		if len(lines) == 0 {
			return "(no messages)", nil
		}
		return strings.Join(lines, "\n"), nil
	}
	return "", fmt.Errorf("unknown tool %q", tool)
}

// ---- running it -----------------------------------------------------------------------

func runSlackHub(home string) error {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	sock := hubSocketPath(home)
	if hubRunning(home) {
		return fmt.Errorf("a Slack hub is already running on %s", sock)
	}
	_ = os.Remove(sock) // stale, from a hub that died
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	defer ln.Close()
	_ = os.Chmod(sock, 0o600)

	// The tokens may not be in yet (setup starts the daemon first): wait for them.
	var cfg slackConfig
	for {
		cfg, err = loadSlackConfig(home)
		if err == nil {
			break
		}
		fmt.Fprintf(os.Stderr, "Slack hub: %v; checking again in a minute.\n", err)
		time.Sleep(time.Minute)
	}
	api := slack.New(cfg.botToken, slack.OptionAppLevelToken(cfg.appToken))
	rs := &realSlack{api: api, names: map[string]string{}}
	h := newHub(home, cfg, rs)
	auth, err := api.AuthTest()
	if err != nil {
		return fmt.Errorf("Slack rejected the bot token: %w", err)
	}
	h.botUser = auth.UserID
	h.log("Slack hub for %s as @%s; %d link(s); listening on %s", auth.Team, auth.User, len(h.links), sock)

	go func() {
		for range time.Tick(2 * time.Second) {
			h.reloadLinks()
		}
	}()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				h.log("accept: %v", err)
				time.Sleep(time.Second)
				continue
			}
			go h.serveShim(conn)
		}
	}()

	for {
		err := h.runSocketMode(api, auth.UserID)
		h.mu.Lock()
		h.slackText = "disconnected"
		h.mu.Unlock()
		h.log("Slack connection ended: %v; reconnecting in 10s", err)
		time.Sleep(10 * time.Second)
	}
}

func (h *hub) runSocketMode(api *slack.Client, botUser string) error {
	client := socketmode.New(api)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.RunContext(ctx) }()
	seen := map[string]bool{}
	var order []string
	for {
		select {
		case err := <-done:
			return err
		case evt := <-client.Events:
			switch evt.Type {
			case socketmode.EventTypeConnected:
				h.mu.Lock()
				h.slackText = "connected"
				h.mu.Unlock()
				h.log("connected to Slack")
			case socketmode.EventTypeInvalidAuth:
				return errors.New("Slack rejected the app token")
			case socketmode.EventTypeEventsAPI:
				if evt.Request != nil {
					client.Ack(*evt.Request)
				}
				ev, ok := evt.Data.(slackevents.EventsAPIEvent)
				if !ok {
					continue
				}
				if j, ok := ev.InnerEvent.Data.(*slackevents.MemberJoinedChannelEvent); ok {
					go h.onJoined(j.User, j.Channel, j.Inviter)
					continue
				}
				m, ok := ev.InnerEvent.Data.(*slackevents.MessageEvent)
				// Edits, deletions, joins and bot posts (ours too) are not someone talking.
				if !ok || m.SubType != "" || m.BotID != "" || m.User == "" || m.User == botUser {
					continue
				}
				// Slack redelivers what it thinks went unacknowledged.
				k := m.Channel + "/" + m.TimeStamp
				if seen[k] {
					continue
				}
				seen[k] = true
				if order = append(order, k); len(order) > 1000 {
					delete(seen, order[0])
					order = order[1:]
				}
				h.onMessage(slackMessage{channel: m.Channel, user: m.User, text: m.Text, ts: m.TimeStamp, thread: m.ThreadTimeStamp})
			case socketmode.EventTypeSlashCommand:
				cmd, ok := evt.Data.(slack.SlashCommand)
				if !ok || evt.Request == nil {
					continue
				}
				reply := "Unknown command."
				if cmd.Command == slackCommand {
					reply = h.onCommand(cmd.UserID, cmd.ChannelID, cmd.Text)
				}
				client.Ack(*evt.Request, map[string]any{"response_type": "ephemeral", "text": reply})
			case socketmode.EventTypeInteractive:
				if evt.Request != nil {
					client.Ack(*evt.Request)
				}
				if cb, ok := evt.Data.(slack.InteractionCallback); ok {
					h.onInteraction(cb)
				}
			}
		}
	}
}
