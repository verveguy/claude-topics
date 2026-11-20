package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/slack-go/slack"
)

func TestParseSlackChannel(t *testing.T) {
	for in, want := range map[string]string{
		"C0CL9STFXBJ": "C0CL9STFXBJ",
		"https://zorktalk.slack.com/archives/C0CL9STFXBJ":                   "C0CL9STFXBJ",
		"https://zorktalk.slack.com/archives/C0CL9STFXBJ/p1763599999000100": "C0CL9STFXBJ",
		" G01ABCDEFG ": "G01ABCDEFG",
	} {
		got, err := parseSlackChannel(in)
		if err != nil || got != want {
			t.Errorf("parseSlackChannel(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "#general", "U042Q1234", "https://example.com/x"} {
		if _, err := parseSlackChannel(bad); err == nil {
			t.Errorf("parseSlackChannel(%q) accepted it", bad)
		}
	}
}

func TestChannelSourceArgs(t *testing.T) {
	const s = "server:slack"
	cases := []struct{ in, with, without []string }{
		{nil, []string{channelFlag, s}, nil},
		{[]string{channelFlag, "server:fabrik"}, []string{channelFlag, "server:fabrik", s}, []string{channelFlag, "server:fabrik"}},
		{[]string{channelFlag, "server:fabrik", "--model", "opus"}, []string{channelFlag, "server:fabrik", s, "--model", "opus"}, nil},
		{[]string{channelFlag + "=server:fabrik"}, []string{channelFlag + "=server:fabrik," + s}, nil},
		{[]string{"--model", "opus"}, []string{"--model", "opus", channelFlag, s}, nil},
	}
	for _, c := range cases {
		got := withChannelSource(c.in, s)
		if !reflect.DeepEqual(got, c.with) {
			t.Errorf("withChannelSource(%q) = %q, want %q", c.in, got, c.with)
		}
		if again := withChannelSource(got, s); !reflect.DeepEqual(again, got) {
			t.Errorf("adding twice changed %q to %q", got, again)
		}
		back := withoutChannelSource(got, s)
		if len(back) == 0 && len(c.in) == 0 {
			continue
		}
		if !reflect.DeepEqual(back, c.in) {
			t.Errorf("withoutChannelSource(%q) = %q, want %q", got, back, c.in)
		}
	}
}

// ---- hub ------------------------------------------------------------------------------

type fakePost struct {
	channel, thread, text string
	blocks                []slack.Block
}

type fakeSlack struct {
	mu        sync.Mutex
	posts     []fakePost
	reactions []string // "channel/ts:name"
	updates   []string
	n         int
}

func (f *fakeSlack) post(channel, thread, text string, blocks []slack.Block) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	f.posts = append(f.posts, fakePost{channel, thread, text, blocks})
	return fmt.Sprintf("900.%d", f.n), nil
}
func (f *fakeSlack) update(channel, ts, text string, _ []slack.Block) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, ts+":"+text)
	return nil
}
func (f *fakeSlack) react(channel, ts, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reactions = append(f.reactions, channel+"/"+ts+":"+name)
	return nil
}
func (f *fakeSlack) ephemeral(string, string, string) error               { return nil }
func (f *fakeSlack) history(string, string, int) ([]slack.Message, error) { return nil, nil }
func (f *fakeSlack) userName(id string) string                            { return "name-" + id }

func (f *fakeSlack) snapshot() ([]fakePost, []string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakePost{}, f.posts...), append([]string{}, f.reactions...), append([]string{}, f.updates...)
}

// testShim is the hub's view of a shim, driven by hand over a pipe.
type testShim struct {
	conn net.Conn
	in   chan hubWire
}

func connectShim(t *testing.T, h *hub, topic, configDir string) *testShim {
	t.Helper()
	client, server := net.Pipe()
	go h.serveShim(server)
	s := &testShim{conn: client, in: make(chan hubWire, 50)}
	go func() {
		dec := json.NewDecoder(client)
		for {
			var w hubWire
			if dec.Decode(&w) != nil {
				close(s.in)
				return
			}
			s.in <- w
		}
	}()
	s.send(t, hubWire{Type: "hello", Topic: topic, ConfigDir: configDir})
	return s
}

func (s *testShim) send(t *testing.T, w hubWire) {
	t.Helper()
	if err := json.NewEncoder(s.conn).Encode(w); err != nil {
		t.Fatal(err)
	}
}

func (s *testShim) next(t *testing.T) hubWire {
	t.Helper()
	select {
	case w := <-s.in:
		return w
	case <-time.After(2 * time.Second):
		t.Fatal("shim got nothing from the hub")
	}
	return hubWire{}
}

func newTestHub(t *testing.T, links ...slackLink) (*hub, *fakeSlack) {
	t.Helper()
	home := t.TempDir()
	if err := saveLinks(home, links); err != nil {
		t.Fatal(err)
	}
	fake := &fakeSlack{}
	h := newHub(home, slackConfig{allowed: map[string]bool{"UME": true}}, fake)
	h.log = func(string, ...any) {}
	h.topicUp = func(slackLink) bool { return false }
	h.startTopic = func(slackLink) error { return nil }
	return h, fake
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestHubRoutesEachChannelToItsTopic(t *testing.T) {
	h, fake := newTestHub(t,
		slackLink{Channel: "CA", Topic: "Alpha", ConfigDir: "/p"},
		slackLink{Channel: "CB", Topic: "Beta", ConfigDir: "/p"})
	a := connectShim(t, h, "Alpha", "/p")
	if w := a.next(t); w.Type != "welcome" || w.Channel != "CA" {
		t.Fatalf("Alpha's welcome = %+v", w)
	}
	b := connectShim(t, h, "Beta", "/p")
	if w := b.next(t); w.Channel != "CB" {
		t.Fatalf("Beta's welcome = %+v", w)
	}
	other := connectShim(t, h, "Gamma", "/p")
	if w := other.next(t); w.Type != "welcome" || w.Channel != "" {
		t.Fatalf("an unlinked topic's welcome = %+v, want no channel", w)
	}

	h.onMessage(slackMessage{channel: "CB", user: "UME", text: "hi beta", ts: "1.1", thread: "1.0"})
	w := b.next(t)
	if w.Type != "inbound" || w.Content != "hi beta" || w.Meta["thread_ts"] != "1.0" || w.Meta["user"] != "name-UME" {
		t.Fatalf("Beta got %+v", w)
	}
	select {
	case w := <-a.in:
		t.Fatalf("Alpha got Beta's message: %+v", w)
	case <-time.After(50 * time.Millisecond):
	}
	if _, r, _ := fake.snapshot(); !reflect.DeepEqual(r, []string{"CB/1.1:eyes"}) {
		t.Errorf("reactions = %q, want the eyes receipt", r)
	}

	// Strangers reach nobody; an allowed user in a channel with no topic is told how
	// to link it, once, rather than reaching anyone.
	h.onMessage(slackMessage{channel: "CA", user: "USTRANGER", text: "let me in", ts: "2.1"})
	h.onMessage(slackMessage{channel: "CZ", user: "UME", text: "anyone?", ts: "2.2"})
	h.onMessage(slackMessage{channel: "CZ", user: "UME", text: "hello?", ts: "2.3"})
	select {
	case w := <-a.in:
		t.Fatalf("Alpha got %+v", w)
	case <-time.After(50 * time.Millisecond):
	}
	if p, _, _ := fake.snapshot(); len(p) != 1 || p[0].channel != "CZ" || !strings.Contains(p[0].text, "/topics new") {
		t.Fatalf("posts = %+v, want one prompt in CZ", p)
	}

	// Tools act on the caller's own channel only.
	if _, err := h.runTool(topicKey("/p", "Alpha"), "reply", map[string]any{"text": "hello"}); err != nil {
		t.Fatal(err)
	}
	if p, _, _ := fake.snapshot(); len(p) != 2 || p[1].channel != "CA" {
		t.Errorf("reply posted %+v, want a post to CA", p)
	}
	if _, err := h.runTool(topicKey("/p", "Gamma"), "reply", map[string]any{"text": "x"}); err == nil {
		t.Error("an unlinked topic could post")
	}
}

func TestHubHoldsMessagesForADownTopicAndStartsItOnUp(t *testing.T) {
	h, fake := newTestHub(t, slackLink{Channel: "CA", Topic: "Alpha", ConfigDir: "/p"})
	started := make(chan slackLink, 1)
	h.startTopic = func(l slackLink) error { started <- l; return nil }

	h.onMessage(slackMessage{channel: "CA", user: "UME", text: "first", ts: "1.1"})
	h.onMessage(slackMessage{channel: "CA", user: "UME", text: "second", ts: "1.2"})
	posts, _, _ := fake.snapshot()
	if len(posts) != 1 || !strings.Contains(posts[0].text, "is down") || !strings.Contains(posts[0].text, "`up`") {
		t.Fatalf("posts = %+v, want one down notice", posts)
	}

	h.onMessage(slackMessage{channel: "CA", user: "UME", text: " Up ", ts: "1.3"})
	select {
	case l := <-started:
		if l.Topic != "Alpha" || l.ConfigDir != "/p" {
			t.Fatalf("started %+v", l)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("`up` did not start the topic")
	}

	// Once its session listens, it gets what was said while it was down, but not "up".
	a := connectShim(t, h, "Alpha", "/p")
	a.next(t) // welcome
	for _, want := range []string{"first", "second"} {
		w := a.next(t)
		if w.Content != want || w.Meta["held"] != "true" {
			t.Fatalf("held delivery = %+v, want %q", w, want)
		}
	}
	h.onMessage(slackMessage{channel: "CA", user: "UME", text: "third", ts: "1.4"})
	if w := a.next(t); w.Content != "third" || w.Meta["held"] != "" {
		t.Fatalf("live delivery = %+v", w)
	}
}

func TestHubSaysARunningTopicIsNotListening(t *testing.T) {
	h, fake := newTestHub(t, slackLink{Channel: "CA", Topic: "Alpha", ConfigDir: "/p"})
	h.topicUp = func(slackLink) bool { return true }
	started := false
	h.startTopic = func(slackLink) error { started = true; return nil }
	h.onMessage(slackMessage{channel: "CA", user: "UME", text: "hello?", ts: "1.1"})
	h.onMessage(slackMessage{channel: "CA", user: "UME", text: "up", ts: "1.2"})
	time.Sleep(50 * time.Millisecond)
	posts, _, _ := fake.snapshot()
	if len(posts) != 2 || !strings.Contains(posts[0].text, "not listening") || !strings.Contains(posts[1].text, "already running") {
		t.Fatalf("posts = %+v", posts)
	}
	if started {
		t.Error("`up` relaunched a running topic")
	}
}

func TestHubRelaysPermissionPromptsToAllowedUsersOnly(t *testing.T) {
	h, fake := newTestHub(t,
		slackLink{Channel: "CA", Topic: "Alpha", ConfigDir: "/p"},
		slackLink{Channel: "CB", Topic: "Beta", ConfigDir: "/p"})
	a := connectShim(t, h, "Alpha", "/p")
	a.next(t)
	a.send(t, hubWire{Type: "permission_request", RequestID: "abcde", ToolName: "Bash", Description: "rm -rf build", InputPreview: "{}"})
	waitFor(t, "the prompt", func() bool { p, _, _ := fake.snapshot(); return len(p) == 1 })
	posts, _, _ := fake.snapshot()
	if posts[0].channel != "CA" || len(posts[0].blocks) == 0 || !strings.Contains(posts[0].text, "Bash") {
		t.Fatalf("prompt = %+v", posts[0])
	}

	// A stranger's button press and the right ID in the wrong channel both do nothing.
	h.onInteraction(slack.InteractionCallback{Type: slack.InteractionTypeBlockActions,
		User: slack.User{ID: "USTRANGER"}, Channel: slack.Channel{GroupConversation: slack.GroupConversation{Conversation: slack.Conversation{ID: "CA"}}},
		ActionCallback: slack.ActionCallbacks{BlockActions: []*slack.BlockAction{{ActionID: "perm_allow", Value: "abcde"}}}})
	h.onMessage(slackMessage{channel: "CB", user: "UME", text: "yes abcde", ts: "1.0"})
	select {
	case w := <-a.in:
		t.Fatalf("Alpha got %+v", w)
	case <-time.After(50 * time.Millisecond):
	}

	h.onMessage(slackMessage{channel: "CA", user: "UME", text: "no ABCDE", ts: "1.1"})
	if w := a.next(t); w.Type != "permission" || w.RequestID != "abcde" || w.Behavior != "deny" {
		t.Fatalf("Alpha got %+v, want a deny", w)
	}
	if _, _, u := fake.snapshot(); len(u) != 1 || !strings.Contains(u[0], "Denied") {
		t.Errorf("prompt updates = %q", u)
	}
	// Answered once; a second answer is just a message.
	h.onMessage(slackMessage{channel: "CA", user: "UME", text: "yes abcde", ts: "1.2"})
	if w := a.next(t); w.Type != "inbound" {
		t.Fatalf("a second answer became %+v", w)
	}
}

func TestHubTellsShimsWhenALinkChanges(t *testing.T) {
	h, _ := newTestHub(t)
	a := connectShim(t, h, "Alpha", "/p")
	if w := a.next(t); w.Channel != "" {
		t.Fatalf("welcome = %+v", w)
	}
	time.Sleep(20 * time.Millisecond) // a distinct mtime
	if err := saveLinks(h.home, []slackLink{{Channel: "CA", Topic: "Alpha", ConfigDir: "/p"}}); err != nil {
		t.Fatal(err)
	}
	h.reloadLinks()
	if w := a.next(t); w.Type != "welcome" || w.Channel != "CA" {
		t.Fatalf("after linking, got %+v", w)
	}
}

// ---- shim -----------------------------------------------------------------------------

type shimHarness struct {
	stdin *io.PipeWriter
	out   chan map[string]any
	hub   net.Conn // the hub's end
	hubIn chan hubWire
}

func startShim(t *testing.T, topic string, identify ...func() string) *shimHarness {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	shimEnd, hubEnd := net.Pipe()
	dialed := false
	s := newSlackShim(topic, "/p", outW, func() (net.Conn, error) {
		if dialed {
			return nil, fmt.Errorf("one hub only")
		}
		dialed = true
		return shimEnd, nil
	})
	s.retry = time.Hour
	s.listenDelay, s.listenFallback = 0, time.Hour
	if len(identify) > 0 {
		s.identify = identify[0]
	}
	go s.serve(inR)
	hh := &shimHarness{stdin: inW, out: make(chan map[string]any, 50), hub: hubEnd, hubIn: make(chan hubWire, 50)}
	go func() {
		sc := bufio.NewScanner(outR)
		for sc.Scan() {
			var m map[string]any
			if json.Unmarshal(sc.Bytes(), &m) == nil {
				hh.out <- m
			}
		}
	}()
	go func() {
		dec := json.NewDecoder(hubEnd)
		for {
			var w hubWire
			if dec.Decode(&w) != nil {
				return
			}
			hh.hubIn <- w
		}
	}()
	t.Cleanup(func() { inW.Close(); hubEnd.Close() })
	return hh
}

func (hh *shimHarness) rpc(t *testing.T, id int, method string, params any) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if _, err := hh.stdin.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
}

func (hh *shimHarness) notify(t *testing.T, method string) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method})
	if _, err := hh.stdin.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
}

func (hh *shimHarness) fromHub(t *testing.T, w hubWire) {
	t.Helper()
	if err := json.NewEncoder(hh.hub).Encode(w); err != nil {
		t.Fatal(err)
	}
}

func (hh *shimHarness) next(t *testing.T) map[string]any {
	t.Helper()
	select {
	case m := <-hh.out:
		return m
	case <-time.After(3 * time.Second):
		t.Fatal("shim wrote nothing")
	}
	return nil
}

func TestShimSpeaksChannelMCP(t *testing.T) {
	hh := startShim(t, "Alpha")
	hh.rpc(t, 1, "initialize", map[string]any{"protocolVersion": "2025-11-25"})
	init := hh.next(t)["result"].(map[string]any)
	// Not before the handshake is done: a notification sent earlier is dropped, and the
	// hub hands a newly registered shim its held messages at once.
	select {
	case w := <-hh.hubIn:
		t.Fatalf("registered with the hub before notifications/initialized: %+v", w)
	case <-time.After(50 * time.Millisecond):
	}
	hh.notify(t, "notifications/initialized")
	if w := <-hh.hubIn; w.Type != "hello" || w.Topic != "Alpha" || w.ConfigDir != "/p" {
		t.Fatalf("hello = %+v", w)
	}
	exp := init["capabilities"].(map[string]any)["experimental"].(map[string]any)
	if _, ok := exp["claude/channel"]; !ok || init["protocolVersion"] != "2025-11-25" {
		t.Fatalf("initialize = %v", init)
	}

	hh.fromHub(t, hubWire{Type: "welcome", Channel: "CA"})
	time.Sleep(20 * time.Millisecond)
	hh.rpc(t, 2, "tools/list", nil)
	tools := hh.next(t)["result"].(map[string]any)["tools"].([]any)
	if len(tools) != len(slackTools) {
		t.Fatalf("a linked topic lists %d tools, want %d", len(tools), len(slackTools))
	}

	hh.fromHub(t, hubWire{Type: "inbound", Content: "hello", Meta: map[string]string{"ts": "1.1"}})
	n := hh.next(t)
	if n["method"] != "notifications/claude/channel" || n["params"].(map[string]any)["content"] != "hello" {
		t.Fatalf("inbound became %v", n)
	}

	// A tool call goes to the hub and its answer comes back.
	hh.rpc(t, 3, "tools/call", map[string]any{"name": "reply", "arguments": map[string]any{"text": "hi"}})
	call := <-hh.hubIn
	if call.Type != "call" || call.Tool != "reply" || call.Args["text"] != "hi" {
		t.Fatalf("call = %+v", call)
	}
	hh.fromHub(t, hubWire{Type: "result", ID: call.ID, Text: "posted ts=9.9"})
	res := hh.next(t)
	if res["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"] != "posted ts=9.9" {
		t.Fatalf("result = %v", res)
	}

	// Permission prompts go up, answers come down.
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/claude/channel/permission_request",
		"params": map[string]any{"request_id": "abcde", "tool_name": "Bash", "description": "d", "input_preview": "p"}})
	_, _ = hh.stdin.Write(append(b, '\n'))
	if w := <-hh.hubIn; w.Type != "permission_request" || w.RequestID != "abcde" || w.ToolName != "Bash" {
		t.Fatalf("permission request = %+v", w)
	}
	hh.fromHub(t, hubWire{Type: "permission", RequestID: "abcde", Behavior: "allow"})
	if p := hh.next(t); p["method"] != "notifications/claude/channel/permission" || p["params"].(map[string]any)["behavior"] != "allow" {
		t.Fatalf("permission answer = %v", p)
	}

	// Unlinking hides the tools, and says so.
	hh.fromHub(t, hubWire{Type: "welcome", Channel: ""})
	if c := hh.next(t); c["method"] != "notifications/tools/list_changed" {
		t.Fatalf("after unlinking: %v", c)
	}
	hh.rpc(t, 4, "tools/list", nil)
	if tools := hh.next(t)["result"].(map[string]any)["tools"].([]any); len(tools) != 0 {
		t.Fatalf("an unlinked topic lists %d tools", len(tools))
	}
}

func TestShimOutsideATopicListsNothing(t *testing.T) {
	hh := startShim(t, "")
	hh.notify(t, "notifications/initialized")
	hh.rpc(t, 1, "tools/list", nil)
	if tools := hh.next(t)["result"].(map[string]any)["tools"].([]any); len(tools) != 0 {
		t.Fatalf("listed %d tools outside a topic", len(tools))
	}
	select {
	case w := <-hh.hubIn:
		t.Fatalf("contacted the hub: %+v", w)
	case <-time.After(50 * time.Millisecond):
	}
}

func (f *fakeSlack) canRead(string) error { return nil }

// ---- /topics --------------------------------------------------------------------------

func TestParseTopicCommand(t *testing.T) {
	cases := map[string]topicCommand{
		"":                        {verb: "help"},
		"new Slack Bridge Test 2": {verb: "new", name: "Slack Bridge Test 2"},
		`new “Odd -- Name” ~/dev/x --profile kmba`: {verb: "new", name: "Odd -- Name", dir: "~/dev/x", profile: "kmba"},
		"LINK Fantasy Economics -p v3rv":           {verb: "link", name: "Fantasy Economics", profile: "v3rv"},
		"new Alpha /tmp/a --profile=default":       {verb: "new", name: "Alpha", dir: "/tmp/a", profile: "default"},
	}
	for in, want := range cases {
		got, err := parseTopicCommand(in)
		if err != nil || got != want {
			t.Errorf("parseTopicCommand(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"new A --force", "new A --profile", "new A /x /y"} {
		if _, err := parseTopicCommand(bad); err == nil {
			t.Errorf("parseTopicCommand(%q) accepted it", bad)
		}
	}
}

// commandHub is a test hub whose Slack home sits in a profile dir, with topic commands
// recorded rather than run and the shim registration stubbed out.
func commandHub(t *testing.T) (h *hub, fake *fakeSlack, profiles []string, ran chan []string) {
	t.Helper()
	base := t.TempDir()
	t.Setenv("HOME", base)
	profiles = []string{filepath.Join(base, ".claude"), filepath.Join(base, ".claude-kmba")}
	for _, p := range profiles {
		if err := os.MkdirAll(filepath.Join(p, "topics"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	home := slackHomeFor(profiles[1])
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	fake = &fakeSlack{}
	h = newHub(home, slackConfig{allowed: map[string]bool{"UME": true}}, fake)
	h.log = func(string, ...any) {}
	h.botUser = "UBOT"
	h.topicUp = func(slackLink) bool { return false }
	h.profiles = func() []string { return profiles }
	ran = make(chan []string, 10)
	h.runTopic = func(configDir string, args ...string) (string, error) {
		ran <- append([]string{configDir}, args...)
		return "", nil
	}
	old := registerShim
	registerShim = func(string) (string, error) { return "", nil }
	t.Cleanup(func() { registerShim = old })
	return h, fake, profiles, ran
}

func seedTopic(t *testing.T, configDir, name string) {
	t.Helper()
	if err := cmdSet([]string{topicFile(filepath.Join(configDir, "topics"), name), "name", name, "state", "down"}); err != nil {
		t.Fatal(err)
	}
}

func TestTopicsCommandRefusesStrangers(t *testing.T) {
	h, fake, _, ran := commandHub(t)
	if r := h.onCommand("USTRANGER", "CN", "new Sneaky"); !strings.Contains(r, "Only allowed users") {
		t.Fatalf("reply = %q", r)
	}
	time.Sleep(50 * time.Millisecond)
	if p, _, _ := fake.snapshot(); len(p) != 0 || len(ran) != 0 {
		t.Fatalf("a stranger's command did something: posts %+v, ran %d", p, len(ran))
	}
}

func TestTopicsNewCreatesLinksAndStartsATopic(t *testing.T) {
	h, fake, profiles, ran := commandHub(t)
	r := h.onCommand("UME", "CN", "new Slack Bridge Test 2")
	if !strings.Contains(r, "profile kmba") {
		t.Fatalf("reply = %q, want it to name the hub's profile", r)
	}
	var got []string
	select {
	case got = <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("new did not run topic up")
	}
	dir := filepath.Join(os.Getenv("HOME"), "dev", ".scratch", "slack-topics", "slack-bridge-test-2")
	want := []string{profiles[1], "up", "Slack Bridge Test 2", dir, "--", channelFlag, "server:slack"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ran %q, want %q", got, want)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Errorf("the topic's directory was not made: %v", err)
	}
	waitFor(t, "the announcement", func() bool { p, _, _ := fake.snapshot(); return len(p) == 1 })
	if l, ok := h.linkForChannel("CN"); !ok || l.Topic != "Slack Bridge Test 2" || l.ConfigDir != profiles[1] {
		t.Fatalf("link = %+v, %v", l, ok)
	}

	// --profile picks another; a name that exists there is refused.
	seedTopic(t, profiles[0], "Taken")
	if r := h.onCommand("UME", "CO", "new Taken --profile default"); !strings.Contains(r, "already has a topic") {
		t.Fatalf("reply = %q", r)
	}
	if r := h.onCommand("UME", "CN", "new Another"); !strings.Contains(r, "already *Slack Bridge Test 2*") {
		t.Fatalf("new in a linked channel: %q", r)
	}
}

func TestTopicsLinkFindsTheTopicsProfile(t *testing.T) {
	h, fake, profiles, _ := commandHub(t)
	seedTopic(t, profiles[0], "Shared Name")
	seedTopic(t, profiles[1], "Shared Name")
	seedTopic(t, profiles[0], "Only Default")

	if r := h.onCommand("UME", "CL", "link Shared Name"); !strings.Contains(r, "more than one profile") {
		t.Fatalf("ambiguous link: %q", r)
	}
	if r := h.onCommand("UME", "CL", "link Only Default"); r != "Linked." {
		t.Fatalf("link: %q", r)
	}
	if l, _ := h.linkForChannel("CL"); l.ConfigDir != profiles[0] {
		t.Fatalf("linked to %+v", l)
	}
	args := topicClaudeArgs(filepath.Join(profiles[0], "topics"), "Only Default")
	if !reflect.DeepEqual(channelSources(args), []string{"server:slack"}) {
		t.Errorf("claude args = %q, want server:slack turned on", args)
	}
	if p, _, _ := fake.snapshot(); len(p) != 1 || !strings.Contains(p[0].text, "profile default") {
		t.Errorf("announcement = %+v", p)
	}

	if r := h.onCommand("UME", "CL", "unlink"); r != "Unlinked." {
		t.Fatalf("unlink: %q", r)
	}
	if _, ok := h.linkForChannel("CL"); ok {
		t.Error("still linked")
	}
	if args := topicClaudeArgs(filepath.Join(profiles[0], "topics"), "Only Default"); len(channelSources(args)) != 0 {
		t.Errorf("claude args after unlink = %q", args)
	}
	if r := h.onCommand("UME", "CL", "link Shared Name --profile kmba"); r != "Linked." {
		t.Fatalf("link --profile: %q", r)
	}
}

func TestInvitingTheBotPromptsOnlyForAllowedInviters(t *testing.T) {
	h, fake, _, _ := commandHub(t)
	h.onJoined("USOMEONE", "C1", "UME") // someone else joining: not about us
	h.onJoined("UBOT", "C2", "USTRANGER")
	h.onJoined("UBOT", "C3", "UME")
	p, _, _ := fake.snapshot()
	if len(p) != 2 || p[0].channel != "C2" || !strings.Contains(p[0].text, "only take topics from allowed users") ||
		p[1].channel != "C3" || !strings.Contains(p[1].text, "/topics new") {
		t.Fatalf("posts = %+v", p)
	}
}

func TestShimWaitsForANewTopicToBeRegistered(t *testing.T) {
	var mu sync.Mutex
	asked := 0
	hh := startShim(t, "", func() string {
		mu.Lock()
		defer mu.Unlock()
		if asked++; asked < 3 {
			return "" // `topic up` has not written the registry entry yet
		}
		return "Fresh"
	})
	hh.notify(t, "notifications/initialized")
	hh.rpc(t, 1, "tools/list", nil)
	if tools := hh.next(t)["result"].(map[string]any)["tools"].([]any); len(tools) != 0 {
		t.Fatalf("listed %d tools before knowing its topic", len(tools))
	}
	select {
	case w := <-hh.hubIn:
		if w.Type != "hello" || w.Topic != "Fresh" {
			t.Fatalf("hello = %+v", w)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("never registered once the topic existed")
	}
}

func TestShimHoldsMessagesUntilClaudeCanTakeThem(t *testing.T) {
	hh := startShim(t, "Alpha")
	hh.rpc(t, 1, "initialize", map[string]any{})
	hh.next(t)
	hh.notify(t, "notifications/initialized")
	<-hh.hubIn // hello
	hh.fromHub(t, hubWire{Type: "welcome", Channel: "CA"})
	// The hub hands over held messages as soon as the shim registers: before claude
	// has listed tools, and so before it accepts channel notifications.
	hh.fromHub(t, hubWire{Type: "inbound", Content: "said while down", Meta: map[string]string{"held": "true"}})
	select {
	case m := <-hh.out:
		t.Fatalf("passed on before claude could take it: %v", m)
	case <-time.After(100 * time.Millisecond):
	}
	hh.rpc(t, 2, "tools/list", nil)
	if r := hh.next(t); r["result"] == nil {
		t.Fatalf("tools/list = %v", r)
	}
	if n := hh.next(t); n["method"] != "notifications/claude/channel" || n["params"].(map[string]any)["content"] != "said while down" {
		t.Fatalf("after tools/list: %v", n)
	}
}

func (f *fakeSlack) members(string) ([]string, error) { return []string{"UME", "UPAL", "UBOT"}, nil }
func (f *fakeSlack) dm(user, text string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	f.posts = append(f.posts, fakePost{channel: "DM:" + user, text: text})
	return "D" + user, fmt.Sprintf("900.%d", f.n), nil
}

// ---- addressing, catch-up, mentions --------------------------------------------------

func TestMentionModeSendsOnlyWhatIsAddressedWithCatchUp(t *testing.T) {
	h, fake := newTestHub(t, slackLink{Channel: "CA", Topic: "Alpha", ConfigDir: "/p", Mode: modeMention})
	h.botUser = "UBOT"
	a := connectShim(t, h, "Alpha", "/p")
	a.next(t)
	quiet := func(why string) {
		t.Helper()
		select {
		case w := <-a.in:
			t.Fatalf("%s reached the topic: %+v", why, w)
		case <-time.After(50 * time.Millisecond):
		}
	}

	h.onMessage(slackMessage{channel: "CA", user: "UME", text: "shall we ship friday?", ts: "1.1"})
	h.onMessage(slackMessage{channel: "CA", user: "UME", text: "side thread", ts: "1.3", thread: "0.9"})
	quiet("an unaddressed message")
	if _, r, _ := fake.snapshot(); len(r) != 0 {
		t.Errorf("reacted to unaddressed messages: %q", r)
	}

	h.onMessage(slackMessage{channel: "CA", user: "UME", text: "<@UBOT> what do you think?", ts: "1.2"})
	w := a.next(t)
	if !strings.Contains(w.Content, "In the channel (where the message for you was posted):\n  [ts=1.1] name-UME: shall we ship friday?") ||
		!strings.Contains(w.Content, "In the thread thread_ts=0.9:\n  [ts=1.3] name-UME: side thread") ||
		!strings.HasSuffix(w.Content, "what do you think?") || strings.Contains(w.Content, "<@UBOT>") {
		t.Fatalf("addressed message = %q, want the channel's catch-up, threads included, and the message, without the mention", w.Content)
	}

	// Once addressed, its thread is a conversation with the bot: no mention needed.
	h.onMessage(slackMessage{channel: "CA", user: "UME", text: "and saturday?", ts: "1.4", thread: "1.2"})
	if w := a.next(t); w.Content != "and saturday?" {
		t.Fatalf("thread reply = %q", w.Content)
	}
	// The catch-up was spent.
	if n := len(h.unsent["CA"]); n != 0 {
		t.Errorf("%d unsent lines left, want none", n)
	}
}

func TestLimitMentions(t *testing.T) {
	h, _ := newTestHub(t)
	got := h.limitMentions("CA", "<!channel> <!here|here> hi <@UPAL>, <@USTRANGER> and <!subteam^S123|@eng>")
	want := "@channel @here hi <@UPAL>, name-USTRANGER and @eng"
	if got != want {
		t.Fatalf("limitMentions = %q\nwant %q", got, want)
	}
}

func TestNotifyMessagesTheOwnersOnly(t *testing.T) {
	h, fake := newTestHub(t, slackLink{Channel: "CA", Topic: "Alpha", ConfigDir: "/p"})
	if _, err := h.runTool(topicKey("/p", "Alpha"), "notify", map[string]any{"text": "build is green"}); err != nil {
		t.Fatal(err)
	}
	p, _, _ := fake.snapshot()
	if len(p) != 1 || p[0].channel != "DM:UME" || !strings.Contains(p[0].text, "*Alpha*") || !strings.Contains(p[0].text, "build is green") {
		t.Fatalf("posts = %+v, want one DM to the allowed user", p)
	}
}

func TestDMAnswersReachTheTopicThatNotified(t *testing.T) {
	h, fake := newTestHub(t, slackLink{Channel: "CA", Topic: "Alpha", ConfigDir: "/p"}, slackLink{Channel: "CB", Topic: "Beta", ConfigDir: "/p"})
	a := connectShim(t, h, "Alpha", "/p")
	a.next(t)
	b := connectShim(t, h, "Beta", "/p")
	b.next(t)

	h.onMessage(slackMessage{channel: "DUME", user: "UME", text: "hello?", ts: "1.0"})
	if p, _, _ := fake.snapshot(); !strings.Contains(p[len(p)-1].text, "none has yet") {
		t.Fatalf("DM before any notify: %+v", p)
	}

	alpha, beta := topicKey("/p", "Alpha"), topicKey("/p", "Beta")
	if _, err := h.runTool(alpha, "notify", map[string]any{"text": "need a decision"}); err != nil {
		t.Fatal(err)
	}
	alphaTS := "900.2"
	if _, err := h.runTool(beta, "notify", map[string]any{"text": "done"}); err != nil {
		t.Fatal(err)
	}

	// A thread reply goes to whoever sent that notify; a top-level DM to the latest.
	h.onMessage(slackMessage{channel: "DUME", user: "UME", text: "go with plan B", ts: "2.0", thread: alphaTS})
	if w := a.next(t); w.Content != "go with plan B" || w.Meta["dm"] != "true" || w.Meta["channel"] != "DUME" {
		t.Fatalf("Alpha got %+v", w)
	}
	h.onMessage(slackMessage{channel: "DUME", user: "UME", text: "thanks", ts: "3.0"})
	if w := b.next(t); w.Content != "thanks" {
		t.Fatalf("Beta got %+v", w)
	}
	h.onMessage(slackMessage{channel: "DUPAL", user: "UPAL", text: "let me in", ts: "4.0"})
	select {
	case w := <-a.in:
		t.Fatalf("a stranger's DM reached Alpha: %+v", w)
	case w := <-b.in:
		t.Fatalf("a stranger's DM reached Beta: %+v", w)
	case <-time.After(50 * time.Millisecond):
	}

	// Each topic answers in the DM, and only in DMs it notified in.
	if _, err := h.runTool(alpha, "reply", map[string]any{"text": "ok", "channel": "DUME", "thread_ts": alphaTS}); err != nil {
		t.Fatal(err)
	}
	if p, _, _ := fake.snapshot(); p[len(p)-1].channel != "DUME" || p[len(p)-1].text != "ok" {
		t.Fatalf("reply = %+v", p[len(p)-1])
	}
	if _, err := h.runTool(alpha, "reply", map[string]any{"text": "x", "channel": "CB"}); err == nil {
		t.Fatal("Alpha posted in Beta's channel")
	}
}

func TestTopicsModeAndStop(t *testing.T) {
	h, _, profiles, _ := commandHub(t)
	seedTopic(t, profiles[1], "Alpha")
	if r := h.onCommand("UME", "CA", "link Alpha"); r != "Linked." {
		t.Fatalf("link: %q", r)
	}
	if r := h.onCommand("UME", "CA", "mode mention"); r != "Done." {
		t.Fatalf("mode: %q", r)
	}
	if l, _ := h.linkForChannel("CA"); l.Mode != modeMention {
		t.Fatalf("mode = %q", l.Mode)
	}
	if r := h.onCommand("UME", "CA", "status"); !strings.Contains(r, "only @mentions") {
		t.Errorf("status = %q", r)
	}
	if r := h.onCommand("UME", "CA", "mode sometimes"); !strings.Contains(r, "/topics mode all") {
		t.Errorf("bad mode: %q", r)
	}
	h.onCommand("UME", "CA", "mode all")
	if l, _ := h.linkForChannel("CA"); l.Mode != "" {
		t.Fatalf("mode = %q after mode all", l.Mode)
	}

	interrupted := ""
	h.interrupt = func(l slackLink) error { interrupted = l.Topic; return nil }
	if r := h.onCommand("UME", "CA", "stop"); !strings.Contains(r, "not running") || interrupted != "" {
		t.Fatalf("stop on a down topic: %q, interrupted %q", r, interrupted)
	}
	h.topicUp = func(slackLink) bool { return true }
	if r := h.onCommand("UME", "CA", "stop"); r != "Stopped." || interrupted != "Alpha" {
		t.Fatalf("stop: %q, interrupted %q", r, interrupted)
	}
}
