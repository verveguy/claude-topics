package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// /topics: managing topics from Slack. A slash command never reaches the channel as a
// message, so Claude never sees it: control and conversation cannot be confused. The
// built-in /topic sets a channel's description, hence the plural.
//
// Only users in SLACK_ALLOWED_USERS may use it, the same people who can already tell a
// linked topic's Claude to do anything. A command acts on the channel it is typed in.

const slackCommand = "/topics"

const slackCommandHelp = "`/topics new <name> [dir] [--profile p]`  create a topic, link this channel to it, start it\n" +
	"`/topics link <name> [--profile p]`  link this channel to an existing topic\n" +
	"`/topics unlink`  stop routing this channel\n" +
	"`/topics up` · `down` · `restart`  this channel's topic\n" +
	"`/topics stop`  interrupt what it is doing right now\n" +
	"`/topics mode all|mention`  `all`: every message goes to it; `mention`: only messages that @mention the bot, and threads it is in\n" +
	"`/topics status`  what this channel is linked to\n" +
	"Profiles are named as for `topic move --to`: a tag such as `kmba`, `default`, or a path. " +
	"Without --profile, `new` uses the hub's own profile and `link` looks in every profile. " +
	"A new topic's directory defaults to a new folder under ~/dev/.scratch/slack-topics."

// slackCommandArgs splits a command's text into words, honouring quotes, including the
// curly ones Slack's clients like to substitute.
func slackCommandArgs(text string) []string {
	var out []string
	var cur strings.Builder
	in, quote := false, rune(0)
	closing := map[rune]rune{'"': '"', '\'': '\'', '“': '”', '‘': '’'}
	flush := func() {
		if in {
			out = append(out, cur.String())
		}
		cur.Reset()
		in = false
	}
	for _, r := range text {
		switch {
		case quote != 0:
			if r == closing[quote] {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case closing[r] != 0:
			quote, in = r, true
		case unicode.IsSpace(r):
			flush()
		default:
			cur.WriteRune(r)
			in = true
		}
	}
	flush()
	return out
}

type topicCommand struct {
	verb, name, dir, profile string
}

// parseTopicCommand reads `<verb> [name words...] [dir] [--profile p]`. A name needs no
// quotes: the words that are not a flag or a path are the name. A path is a word that
// starts with /, ~ or ./.
func parseTopicCommand(text string) (topicCommand, error) {
	words := slackCommandArgs(text)
	if len(words) == 0 {
		return topicCommand{verb: "help"}, nil
	}
	c := topicCommand{verb: strings.ToLower(words[0])}
	var name []string
	for i := 1; i < len(words); i++ {
		w := words[i]
		switch {
		case w == "--profile" || w == "-p":
			if i+1 >= len(words) {
				return c, errors.New("--profile needs a value: a tag such as kmba, default, or a path")
			}
			i++
			c.profile = words[i]
		case strings.HasPrefix(w, "--profile="):
			c.profile = strings.TrimPrefix(w, "--profile=")
		case strings.HasPrefix(w, "/") || strings.HasPrefix(w, "~") || strings.HasPrefix(w, "./"):
			if c.dir != "" {
				return c, fmt.Errorf("two directories: %s and %s", c.dir, w)
			}
			c.dir = w
		case strings.HasPrefix(w, "-"):
			return c, fmt.Errorf("unknown option %s", w)
		default:
			name = append(name, w)
		}
	}
	c.name = strings.Join(name, " ")
	return c, nil
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
}

// hubProfile is the profile the Slack home belongs to: <config dir>/channels/slack.
func (h *hub) hubProfile() string { return filepath.Dir(filepath.Dir(h.home)) }

// findTopicProfile finds which profile holds a topic: the one named, or the only one
// with a topic of that name.
func (h *hub) findTopicProfile(name, profileFlag string) (string, error) {
	if profileFlag != "" {
		cfg := resolveProfile(profileFlag)
		if !fileExists(topicFile(filepath.Join(cfg, "topics"), name)) {
			return "", fmt.Errorf("no topic %q in profile %s", name, profileLabel(cfg))
		}
		return cfg, nil
	}
	var found []string
	for _, p := range h.profiles() {
		if fileExists(topicFile(filepath.Join(p, "topics"), name)) {
			found = append(found, p)
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("no topic named %q in any profile (`/topics new %s` makes one)", name, name)
	case 1:
		return found[0], nil
	}
	labels := make([]string, len(found))
	for i, p := range found {
		labels[i] = profileLabel(p)
	}
	return "", fmt.Errorf("%q is in more than one profile (%s): add --profile", name, strings.Join(labels, ", "))
}

func profileLabel(configDir string) string {
	if t := profileTag(configDir); t != "" {
		return t
	}
	return "default"
}

// onCommand handles /topics. It returns the immediate answer, shown only to the person
// who typed it; slow work answers in the channel when it is done.
func (h *hub) onCommand(user, channel, text string) string {
	if !h.allowed[user] {
		return "Only allowed users can manage topics from Slack."
	}
	c, err := parseTopicCommand(text)
	if err != nil {
		return err.Error() + "\n" + slackCommandHelp
	}
	link, linked := h.linkForChannel(channel)
	needLink := func() string {
		return "This channel is not linked to a topic. `/topics new <name>` or `/topics link <name>`."
	}
	switch c.verb {
	case "help":
		return slackCommandHelp
	case "status":
		if !linked {
			return needLink()
		}
		h.mu.Lock()
		listening := h.shims[link.key()] != nil
		h.mu.Unlock()
		state := "down"
		switch {
		case listening:
			state = "running and listening"
		case h.topicUp(link):
			state = "running, but not listening (needs `/topics restart`)"
		}
		mode := "every message goes to it"
		if link.Mode == modeMention {
			mode = "only @mentions and its threads go to it"
		}
		return fmt.Sprintf("This channel is *%s* (profile %s): %s; %s.", link.Topic, profileLabel(link.ConfigDir), state, mode)
	case "mode":
		if !linked {
			return needLink()
		}
		var mode string
		switch strings.ToLower(c.name) {
		case "all":
		case "mention", "mentions":
			mode = modeMention
		default:
			return "`/topics mode all` (every message goes to the topic) or `/topics mode mention` (only @mentions and its threads)"
		}
		if err := setLinkMode(h.home, channel, mode); err != nil {
			return err.Error()
		}
		h.reloadLinks()
		if mode == modeMention {
			h.say(channel, fmt.Sprintf("<@%s> set this channel to mention mode: *%s* hears messages that @mention <@%s>, and replies in threads it is in. The rest reaches it as context when it is next addressed.", user, link.Topic, h.botUser))
		} else {
			h.say(channel, fmt.Sprintf("<@%s> set this channel so every message goes to *%s*.", user, link.Topic))
		}
		return "Done."
	case "stop":
		if !linked {
			return needLink()
		}
		if !h.topicUp(link) {
			return fmt.Sprintf("*%s* is not running.", link.Topic)
		}
		if err := h.interrupt(link); err != nil {
			return "Could not interrupt it: " + err.Error()
		}
		h.say(channel, fmt.Sprintf("<@%s> stopped what *%s* was doing. Its session is intact; say what to do next.", user, link.Topic))
		return "Stopped."
	case "new":
		if c.name == "" {
			return "`/topics new <name> [dir] [--profile p]`"
		}
		if linked {
			return fmt.Sprintf("This channel is already *%s*. `/topics unlink` first.", link.Topic)
		}
		cfg := h.hubProfile()
		if c.profile != "" {
			cfg = resolveProfile(c.profile)
		}
		if _, err := os.Stat(cfg); err != nil {
			return fmt.Sprintf("No profile at %s.", cfg)
		}
		root := filepath.Join(cfg, "topics")
		if fileExists(topicFile(root, c.name)) {
			return fmt.Sprintf("Profile %s already has a topic %q: `/topics link %s`.", profileLabel(cfg), c.name, c.name)
		}
		dir := expandHome(c.dir)
		if dir == "" {
			home, _ := os.UserHomeDir()
			dir = filepath.Join(home, "dev", ".scratch", "slack-topics", slugify(c.name))
		}
		go h.newTopic(user, channel, cfg, c.name, dir)
		return fmt.Sprintf("Creating *%s* in profile %s, in %s…", c.name, profileLabel(cfg), dir)
	case "link":
		if c.name == "" {
			return "`/topics link <name> [--profile p]`"
		}
		cfg, err := h.findTopicProfile(c.name, c.profile)
		if err != nil {
			return err.Error()
		}
		notes, err := linkTopic(h.home, h.slack.canRead, cfg, filepath.Join(cfg, "topics"), c.name, channel)
		if err != nil {
			return err.Error()
		}
		h.reloadLinks()
		h.say(channel, fmt.Sprintf("<@%s> linked this channel to *%s* (profile %s). %s", user, c.name, profileLabel(cfg), strings.Join(notes, " ")))
		return "Linked."
	case "unlink":
		if !linked {
			return needLink()
		}
		if err := unlinkTopic(h.home, func(l slackLink) bool { return l.Channel == channel }); err != nil {
			return err.Error()
		}
		h.reloadLinks()
		h.say(channel, fmt.Sprintf("<@%s> unlinked this channel from *%s*. Messages here no longer reach it.", user, link.Topic))
		return "Unlinked."
	case "up":
		if !linked {
			return needLink()
		}
		if h.topicUp(link) {
			return fmt.Sprintf("*%s* is already running.", link.Topic)
		}
		go h.runAndReport(channel, fmt.Sprintf("<@%s> started *%s*.", user, link.Topic), link.ConfigDir, "up", link.Topic)
		return "Starting…"
	case "down":
		if !linked {
			return needLink()
		}
		go h.runAndReport(channel, fmt.Sprintf("<@%s> put *%s* down. Its session is kept: `/topics up` resumes it.", user, link.Topic),
			link.ConfigDir, "down", link.Topic)
		return "Putting it down…"
	case "restart":
		if !linked {
			return needLink()
		}
		go func() {
			if h.topicUp(link) {
				if _, err := h.runTopic(link.ConfigDir, "down", link.Topic); err != nil {
					h.say(channel, fmt.Sprintf("Could not put *%s* down:\n```%s```", link.Topic, err))
					return
				}
			}
			h.runAndReport(channel, fmt.Sprintf("<@%s> restarted *%s*.", user, link.Topic), link.ConfigDir, "up", link.Topic)
		}()
		return "Restarting…"
	}
	return fmt.Sprintf("Unknown command %q.\n%s", c.verb, slackCommandHelp)
}

func (h *hub) say(channel, text string) {
	if _, err := h.slack.post(channel, "", text, nil); err != nil {
		h.log("posting to %s: %v", channel, err)
	}
}

func (h *hub) runAndReport(channel, success, configDir string, args ...string) {
	if _, err := h.runTopic(configDir, args...); err != nil {
		h.say(channel, fmt.Sprintf("`topic %s` failed:\n```%s```", strings.Join(args, " "), err))
		return
	}
	h.say(channel, success)
}

// newTopic creates a topic linked to channel and starts it. The link comes first, and
// the launch carries server:slack, so the session listens from its first moment.
func (h *hub) newTopic(user, channel, cfg, name, dir string) {
	fail := func(err error) { h.say(channel, fmt.Sprintf("Could not create *%s*: %v", name, err)) }
	if err := h.slack.canRead(channel); err != nil {
		fail(err)
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(err)
		return
	}
	note, err := registerShim(cfg)
	if err != nil {
		fail(err)
		return
	}
	links, err := loadLinks(h.home)
	if err != nil {
		fail(err)
		return
	}
	if err := saveLinks(h.home, append(links, slackLink{Channel: channel, Topic: name, ConfigDir: cfg})); err != nil {
		fail(err)
		return
	}
	h.reloadLinks()
	if _, err := h.runTopic(cfg, "up", name, dir, "--", channelFlag, "server:"+slackServer); err != nil {
		_ = unlinkTopic(h.home, func(l slackLink) bool { return l.Channel == channel })
		h.reloadLinks()
		fail(err)
		return
	}
	msg := fmt.Sprintf("<@%s> created *%s* (profile %s, in `%s`). Talk to it here.", user, name, profileLabel(cfg), dir)
	if note != "" {
		msg += " " + note
	}
	h.say(channel, msg)
}

// onJoined answers the bot being invited to a channel: an allowed inviter is told how
// to set the channel up. Nothing is created until they say so.
func (h *hub) onJoined(user, channel, inviter string) {
	if user != h.botUser {
		return
	}
	if l, ok := h.linkForChannel(channel); ok {
		h.say(channel, fmt.Sprintf("This channel is *%s*. Messages here go to it.", l.Topic))
		return
	}
	if !h.allowed[inviter] {
		h.say(channel, "I only take topics from allowed users, so this channel stays unlinked.")
		return
	}
	h.promptUnlinked(channel, "", true)
}

// promptUnlinked says how to link a channel, at most every ten minutes per channel.
func (h *hub) promptUnlinked(channel, thread string, force bool) {
	key := "unlinked:" + channel
	h.mu.Lock()
	recent := time.Since(h.noticed[key]) < 10*time.Minute
	h.noticed[key] = time.Now()
	h.mu.Unlock()
	if recent && !force {
		return
	}
	text := "This channel isn't linked to a topic yet. `/topics new <name>` starts a new one here; " +
		"`/topics link <name>` connects one you already have. `/topics help` for more."
	if _, err := h.slack.post(channel, thread, text, nil); err != nil {
		h.log("posting to %s: %v", channel, err)
	}
}

// setLinkMode changes how a linked channel addresses its topic.
func setLinkMode(home, channel, mode string) error {
	links, err := loadLinks(home)
	if err != nil {
		return err
	}
	for i := range links {
		if links[i].Channel == channel {
			links[i].Mode = mode
			return saveLinks(home, links)
		}
	}
	return fmt.Errorf("this channel is not linked")
}
