package main

// slack: chat with topics from Slack channels, one channel per topic.
//
//	Slack ──Socket Mode──▶ hub ──unix socket──▶ shim (in topic A's session)
//	                        │                  ▶ shim (in topic B's session)
//	                        └── links.json: channel ↔ (profile, topic)
//
// The hub is the only process connected to Slack. That matters: Socket Mode spreads an
// app's events across its open connections rather than copying them to each, so one
// connection per session would hand each session a random share of the messages. The
// hub holds the tokens, routes each channel's messages to its topic, and runs the Slack
// calls the topics make. It runs as a topic daemon, so the Dispatcher's check keeps it
// alive. Socket Mode is an outbound WebSocket: nothing on this machine is exposed.
//
// The shim is a Claude Code channel: an MCP server, run by claude in every session of a
// profile, that declares claude/channel. It finds its topic with whoami, registers it
// with the hub, and turns hub messages into channel notifications and Claude's tool
// calls into hub requests. It holds no secrets. Pushes only reach a session launched
// with --dangerously-load-development-channels server:slack, which `slack link` adds to
// the topic's claude args.
//
// A message to a topic that is not listening is held, and the channel is told why. For
// a topic that is down, replying `up` starts it, and the held messages follow.
//
// Everything lives in one profile's <config dir>/channels/slack ("the Slack home"):
// .env (tokens and allowed users), links.json, and hub.sock.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// slackServer is the MCP server name the shim is registered under, and so the channel
// source (server:slack) and the source attribute on its messages.
const slackServer = "slack"

const slackHubDaemon = "Slack Hub"

func slackHomeFor(configDir string) string { return filepath.Join(configDir, "channels", "slack") }
func hubSocketPath(home string) string     { return filepath.Join(home, "hub.sock") }
func slackEnvPath(home string) string      { return filepath.Join(home, ".env") }
func slackLinksPath(home string) string    { return filepath.Join(home, "links.json") }

// findSlackHome finds this machine's Slack home: the profile whose channels/slack holds
// a .env. TOPIC_SLACK_HOME overrides, for tests and for a second home.
func findSlackHome() (string, error) {
	if h := os.Getenv("TOPIC_SLACK_HOME"); h != "" {
		return h, nil
	}
	var found []string
	for _, p := range listProfiles() {
		if fileExists(slackEnvPath(slackHomeFor(p))) {
			found = append(found, slackHomeFor(p))
		}
	}
	switch len(found) {
	case 0:
		return "", errors.New("no Slack bridge is set up on this machine (try: topic slack setup)")
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf("more than one Slack home (%s); set TOPIC_SLACK_HOME to choose", strings.Join(found, ", "))
}

// slackConfig is what .env holds. The real environment wins over the file, as it does
// for claude's own channels.
type slackConfig struct {
	botToken, appToken string
	allowed            map[string]bool
}

func loadSlackConfig(home string) (slackConfig, error) {
	vals := map[string]string{}
	if f, err := os.Open(slackEnvPath(home)); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
			if !ok || strings.HasPrefix(k, "#") {
				continue
			}
			vals[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
		f.Close()
	}
	get := func(k string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return vals[k]
	}
	c := slackConfig{botToken: get("SLACK_BOT_TOKEN"), appToken: get("SLACK_APP_TOKEN"), allowed: map[string]bool{}}
	for _, u := range strings.FieldsFunc(get("SLACK_ALLOWED_USERS"), func(r rune) bool { return r == ',' || r == ' ' }) {
		c.allowed[u] = true
	}
	var missing []string
	if !strings.HasPrefix(c.botToken, "xoxb-") || strings.Contains(c.botToken, "REPLACE") {
		missing = append(missing, "SLACK_BOT_TOKEN (xoxb-…)")
	}
	if !strings.HasPrefix(c.appToken, "xapp-") || strings.Contains(c.appToken, "REPLACE") {
		missing = append(missing, "SLACK_APP_TOKEN (xapp-…)")
	}
	if len(c.allowed) == 0 || c.allowed["UREPLACE"] {
		missing = append(missing, "SLACK_ALLOWED_USERS (U…)")
	}
	if len(missing) > 0 {
		return c, fmt.Errorf("%s needs %s", slackEnvPath(home), strings.Join(missing, ", "))
	}
	return c, nil
}

// slackLink ties one Slack channel to one topic. A topic is named by profile and name,
// since names are unique only within a profile.
type slackLink struct {
	Channel   string `json:"channel"`
	Topic     string `json:"topic"`
	ConfigDir string `json:"configDir"`
	// Mode is how the channel addresses its topic: "" (every message from someone who
	// can direct it goes to it), modeMention (only their messages that mention the bot,
	// and replies in threads it takes part in; the rest arrives as catch-up context) or
	// modeTeam (as mention, but everyone in the channel is heard, labelled with whether
	// they can direct the topic) or modeListen (as team, but every message is sent as
	// it is posted, marked with whether it was addressed to the bot).
	Mode string `json:"mode,omitempty"`
	// Trusted are the Slack users, besides the allowed users, that the topic takes
	// direction from in this channel. Only an allowed user can change it.
	Trusted []string `json:"trusted,omitempty"`
}

const (
	modeMention = "mention"
	modeTeam    = "team"
	modeListen  = "listen"
)

func (l slackLink) key() string { return topicKey(l.ConfigDir, l.Topic) }

func topicKey(configDir, name string) string { return configDir + "\x00" + name }

func loadLinks(home string) ([]slackLink, error) {
	b, err := os.ReadFile(slackLinksPath(home))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f struct {
		Links []slackLink `json:"links"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", slackLinksPath(home), err)
	}
	return f.Links, nil
}

func saveLinks(home string, links []slackLink) error {
	b, err := json.MarshalIndent(map[string]any{"links": links}, "", "  ")
	if err != nil {
		return err
	}
	tmp := slackLinksPath(home) + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, slackLinksPath(home))
}

var slackChannelID = regexp.MustCompile(`^[CG][A-Z0-9]{6,}$`)

// parseSlackChannel takes a channel ID or a channel link (…/archives/C0123ABC[/p…]).
func parseSlackChannel(s string) (string, error) {
	s = strings.TrimSpace(s)
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		for i, p := range parts {
			if p == "archives" && i+1 < len(parts) {
				s = parts[i+1]
				break
			}
		}
	}
	if !slackChannelID.MatchString(s) {
		return "", fmt.Errorf("%q is not a Slack channel ID (C…) or channel link; channel details shows the ID", s)
	}
	return s, nil
}

// withChannelSource returns a claude argument list that turns on channel src, keeping
// every other channel already on: claude takes a single
// --dangerously-load-development-channels whose values are all the sources.
func withChannelSource(a []string, src string) []string {
	for _, s := range channelSources(a) {
		if s == src {
			return a
		}
	}
	out := append([]string{}, a...)
	for i := 0; i < len(out); i++ {
		if out[i] == channelFlag {
			j := i + 1
			for j < len(out) && !strings.HasPrefix(out[j], "-") {
				j++
			}
			return append(out[:j], append([]string{src}, out[j:]...)...)
		}
		if v, ok := strings.CutPrefix(out[i], channelFlag+"="); ok {
			out[i] = channelFlag + "=" + v + "," + src
			return out
		}
	}
	return append(out, channelFlag, src)
}

// withoutChannelSource is withChannelSource's inverse; the flag goes too when src was
// its only source.
func withoutChannelSource(a []string, src string) []string {
	var out []string
	for i := 0; i < len(a); i++ {
		switch v, eq := strings.CutPrefix(a[i], channelFlag+"="); {
		case a[i] == channelFlag:
			var kept []string
			for i+1 < len(a) && !strings.HasPrefix(a[i+1], "-") {
				i++
				if a[i] != src {
					kept = append(kept, a[i])
				}
			}
			if len(kept) > 0 {
				out = append(append(out, channelFlag), kept...)
			}
		case eq:
			var kept []string
			for _, s := range strings.Split(v, ",") {
				if s != src {
					kept = append(kept, s)
				}
			}
			if len(kept) > 0 {
				out = append(out, channelFlag+"="+strings.Join(kept, ","))
			}
		default:
			out = append(out, a[i])
		}
	}
	return out
}

// ---- CLI ------------------------------------------------------------------------------

const slackUsage = `usage:
  topic slack setup                     set up the Slack home in this profile
  topic slack link "<topic>" <channel>  route a Slack channel (ID or link) to a topic
  topic slack unlink "<topic>"          stop routing to it
  topic slack status                    the hub, the links, and who is listening
  topic slack hub [--home <dir>]        run the hub (normally as the "Slack Hub" daemon)
  topic slack shim                      the per-session channel server (run by claude)`

// slack <verb> ... <config-dir> <topics-root>
func cmdSlack(args []string) error {
	if len(args) < 3 {
		return errors.New(slackUsage)
	}
	configDir, topicsRoot := args[len(args)-2], args[len(args)-1]
	verb, rest := args[0], args[1:len(args)-2]
	switch verb {
	case "hub":
		home := ""
		if len(rest) == 2 && rest[0] == "--home" {
			home = rest[1]
		} else if len(rest) != 0 {
			return errors.New(slackUsage)
		}
		if home == "" {
			h, err := findSlackHome()
			if err != nil {
				return err
			}
			home = h
		}
		return runSlackHub(home)
	case "shim":
		return runSlackShim(configDir, topicsRoot)
	case "setup":
		return slackSetup(configDir)
	case "link":
		if len(rest) != 2 {
			return errors.New(`usage: topic slack link "<topic>" <channel ID or link>`)
		}
		return slackLinkTopic(configDir, topicsRoot, rest[0], rest[1])
	case "unlink":
		if len(rest) != 1 {
			return errors.New(`usage: topic slack unlink "<topic>"`)
		}
		return slackUnlinkTopic(configDir, topicsRoot, rest[0])
	case "status":
		return slackStatus()
	}
	return errors.New(slackUsage)
}

const slackManifest = `# Paste at https://api.slack.com/apps -> Create New App -> From a manifest.
display_information:
  name: Claude Topics
  description: Chat with running Claude Code topics, one channel per topic.
features:
  bot_user:
    display_name: Claude
    always_online: true
  slash_commands:
    - command: /topics
      description: Create, link and manage the topic behind this channel
      usage_hint: "new <name> [dir] [--profile p] | link <name> | unlink | up | down | restart | stop | mode | trust @someone | status"
      should_escape: true      # @mentions arrive as user IDs, for /topics trust
  app_home:                # lets you answer notify's DMs
    home_tab_enabled: false
    messages_tab_enabled: true
    messages_tab_read_only_enabled: false
oauth_config:
  scopes:
    bot:
      - channels:history
      - channels:read      # see the bot being invited to a channel
      - groups:history
      - groups:read
      - chat:write
      - commands           # /topics
      - im:history         # answers to those DMs
      - im:write           # notify: a DM to the allowed users
      - reactions:write
      - users:read
settings:
  event_subscriptions:
    bot_events:
      - message.channels
      - message.groups
      - message.im
      - member_joined_channel
  interactivity:
    is_enabled: true
  socket_mode_enabled: true
  org_deploy_enabled: false
  token_rotation_enabled: false
`

func slackSetup(configDir string) error {
	home := slackHomeFor(configDir)
	if existing, err := findSlackHome(); err == nil && existing != home {
		return fmt.Errorf("this machine's Slack home is already %s; run setup in that profile", existing)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	manifest := filepath.Join(home, "slack-app-manifest.yaml")
	if err := os.WriteFile(manifest, []byte(slackManifest), 0o600); err != nil {
		return err
	}
	if !fileExists(slackEnvPath(home)) {
		tmpl := "SLACK_BOT_TOKEN=xoxb-REPLACE\nSLACK_APP_TOKEN=xapp-REPLACE\nSLACK_ALLOWED_USERS=UREPLACE\n"
		if err := os.WriteFile(slackEnvPath(home), []byte(tmpl), 0o600); err != nil {
			return err
		}
	}
	bin, err := topicScript()
	if err != nil {
		return err
	}
	if err := cmdDaemon([]string{"add", slackHubDaemon, home, bin, "slack", "hub", "--home", home, configDir}); err != nil {
		return err
	}
	fmt.Printf(`
Slack home: %s

1. At https://api.slack.com/apps: Create New App -> From a manifest, paste
   %s, and install it to the workspace.
2. Fill in %s:
     SLACK_BOT_TOKEN      OAuth & Permissions -> Bot User OAuth Token (xoxb-…)
     SLACK_APP_TOKEN      Basic Information -> App-Level Tokens, scope connections:write (xapp-…)
     SLACK_ALLOWED_USERS  your member ID: Slack profile -> ⋯ -> Copy member ID (U…)
3. Invite the bot to a channel, then: topic slack link "<topic>" <channel link>

The hub runs as the %q daemon; it waits for the tokens and connects once they are in.
`, home, manifest, slackEnvPath(home), slackHubDaemon)
	return nil
}

func slackLinkTopic(configDir, topicsRoot, name, channelArg string) error {
	channel, err := parseSlackChannel(channelArg)
	if err != nil {
		return err
	}
	home, err := findSlackHome()
	if err != nil {
		return err
	}
	cfg, err := loadSlackConfig(home)
	if err != nil {
		return err
	}
	notes, err := linkTopic(home, newRealSlack(cfg.botToken).canRead, configDir, topicsRoot, name, channel)
	if err != nil {
		return err
	}
	fmt.Printf("Linked %s to %q.\n", channel, name)
	for _, n := range notes {
		fmt.Println("  " + n)
	}
	if !hubRunning(home) {
		fmt.Printf("  The hub is not running: topic daemon start %q (in the profile that owns %s)\n", slackHubDaemon, home)
	}
	return nil
}

// linkTopic routes channel to a topic: links.json, the shim in the topic's profile, and
// server:slack in its claude args. Shared by `topic slack link` and `/topics link`; it
// returns what the caller should tell the person.
func linkTopic(home string, canRead func(string) error, configDir, topicsRoot, name, channel string) ([]string, error) {
	if !fileExists(topicFile(topicsRoot, name)) {
		return nil, fmt.Errorf("no such topic: %q", name)
	}
	if err := canRead(channel); err != nil {
		return nil, err
	}
	links, err := loadLinks(home)
	if err != nil {
		return nil, err
	}
	var notes []string
	l := slackLink{Channel: channel, Topic: name, ConfigDir: configDir}
	var kept []slackLink
	for _, old := range links {
		switch {
		case old.Channel == channel && old.key() != l.key():
			notes = append(notes, fmt.Sprintf("This channel was routed to %q; now it goes to %q.", old.Topic, name))
		case old.key() == l.key() && old.Channel != channel:
			notes = append(notes, fmt.Sprintf("%q was linked to %s; one channel per topic, so that link is replaced.", name, old.Channel))
		case old.key() == l.key() || old.Channel == channel:
		default:
			kept = append(kept, old)
		}
	}
	if err := saveLinks(home, append(kept, l)); err != nil {
		return nil, err
	}
	note, err := registerShim(configDir)
	if err != nil {
		return nil, err
	}
	if note != "" {
		notes = append(notes, note)
	}
	before := topicClaudeArgs(topicsRoot, name)
	after := withChannelSource(before, "server:"+slackServer)
	if err := setClaudeArgs(topicsRoot, name, after); err != nil {
		return nil, err
	}
	switch {
	case len(after) != len(before) && isUp(profileTag(configDir), name):
		notes = append(notes, fmt.Sprintf("It is running without the Slack channel, and starts listening at its next launch: "+
			"/topics restart here, or topic down %q && topic up %q.", name, name))
	case !isUp(profileTag(configDir), name):
		notes = append(notes, "It is down. A message in the channel says so, and replying `up` (or /topics up) starts it.")
	}
	return notes, nil
}

func slackUnlinkTopic(configDir, topicsRoot, name string) error {
	home, err := findSlackHome()
	if err != nil {
		return err
	}
	if err := unlinkTopic(home, func(l slackLink) bool { return l.key() == topicKey(configDir, name) }); err != nil {
		return fmt.Errorf("%q: %w", name, err)
	}
	fmt.Printf("Unlinked %q. Its channel no longer reaches it.\n", name)
	return nil
}

// unlinkTopic drops the links match picks, and server:slack from those topics' args.
func unlinkTopic(home string, match func(slackLink) bool) error {
	links, err := loadLinks(home)
	if err != nil {
		return err
	}
	var kept, gone []slackLink
	for _, l := range links {
		if match(l) {
			gone = append(gone, l)
		} else {
			kept = append(kept, l)
		}
	}
	if len(gone) == 0 {
		return errors.New("not linked to a Slack channel")
	}
	if err := saveLinks(home, kept); err != nil {
		return err
	}
	for _, l := range gone {
		root := filepath.Join(l.ConfigDir, "topics")
		if fileExists(topicFile(root, l.Topic)) {
			if err := setClaudeArgs(root, l.Topic, withoutChannelSource(topicClaudeArgs(root, l.Topic), "server:"+slackServer)); err != nil {
				return err
			}
		}
	}
	return nil
}

// registerShim is ensureShimRegistered, swappable for tests (it runs claude).
var registerShim = ensureShimRegistered

// ensureShimRegistered adds the shim to the profile's user-scope MCP servers, once. The
// shim lists no tools in a session whose topic has no channel, so it costs the other
// sessions nothing but an idle process. It returns a note worth passing on, if any.
func ensureShimRegistered(configDir string) (string, error) {
	get := exec.Command("claude", "mcp", "get", slackServer)
	get.Env = envForProfile(configDir)
	if out, err := get.CombinedOutput(); err == nil {
		if !strings.Contains(string(out), "slack shim") {
			return fmt.Sprintf("An MCP server named %q already exists in %s and is not the topic shim; "+
				"remove it (claude mcp remove %s) for the link to work.", slackServer, configDir, slackServer), nil
		}
		return "", nil
	}
	bin, err := topicScript()
	if err != nil {
		return "", err
	}
	add := exec.Command("claude", "mcp", "add", "-s", "user", slackServer, "--", bin, "slack", "shim")
	add.Env = envForProfile(configDir)
	if out, err := add.CombinedOutput(); err != nil {
		return "", fmt.Errorf("registering the Slack shim in %s: %v\n%s", configDir, err, out)
	}
	return fmt.Sprintf("Registered the Slack shim as MCP server %q in %s.", slackServer, configDir), nil
}

func hubRunning(home string) bool {
	c, err := net.DialTimeout("unix", hubSocketPath(home), time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func slackStatus() error {
	home, err := findSlackHome()
	if err != nil {
		return err
	}
	fmt.Printf("Slack home: %s\n", home)
	if _, err := loadSlackConfig(home); err != nil {
		fmt.Printf("  not configured: %v\n", err)
	}
	listening := map[string]bool{}
	if c, err := net.DialTimeout("unix", hubSocketPath(home), time.Second); err != nil {
		fmt.Println("Hub: not running")
	} else {
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		_ = json.NewEncoder(c).Encode(hubWire{Type: "status"})
		var w hubWire
		if json.NewDecoder(c).Decode(&w) == nil {
			fmt.Printf("Hub: running; Slack %s\n", w.Text)
			for _, k := range w.Listening {
				listening[k] = true
			}
		}
		c.Close()
	}
	links, err := loadLinks(home)
	if err != nil {
		return err
	}
	if len(links) == 0 {
		fmt.Println("No links yet: topic slack link \"<topic>\" <channel>")
		return nil
	}
	for _, l := range links {
		state := "down"
		switch {
		case listening[l.key()]:
			state = "listening"
		case isUp(profileTag(l.ConfigDir), l.Topic):
			state = "up, not listening (relaunch to pick up the channel)"
		}
		where := profileTag(l.ConfigDir)
		if where == "" {
			where = "default"
		}
		fmt.Printf("  %s  →  %q (%s)  %s\n", l.Channel, l.Topic, where, state)
	}
	return nil
}
