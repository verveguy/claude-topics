package main

// Per-topic extra `claude` arguments, persisted as claudeArgs in topic.json.
//
// Some claude settings are per-process and can only be given at launch — MCP channel
// pushes (Fabrik's subscribed-channel updates) are the motivating case: without
// --dangerously-load-development-channels on the command line, Claude Code silently
// drops the pushes. A topic's session is relaunched by up, handoff, fork, move and
// rename, so a flag typed once by hand would be lost at the first `topic down`. The
// list lives in the registry instead, and every launch adds it.

import (
	"fmt"
	"os"
	"strings"
)

// ownedClaudeFlags are the arguments topic sets itself on every launch. A user copy
// would fight ours over which session runs, or under what name.
var ownedClaudeFlags = []string{
	"--resume", "-r", "--session-id", "--name", "-n", "--remote-control", "--continue", "-c",
}

// channelFlag is the research-preview switch that lets MCP channels push into a
// session. Named once, so it is one edit when channels leave the preview.
const channelFlag = "--dangerously-load-development-channels"

func checkClaudeArgs(a []string) error {
	for _, arg := range a {
		for _, f := range ownedClaudeFlags {
			if arg == f || strings.HasPrefix(arg, f+"=") {
				return fmt.Errorf("%s is set by topic itself and cannot be passed through", f)
			}
		}
	}
	return nil
}

// splitClaudeArgs separates topic's own arguments from those after "--", which are
// claude's. given reports whether a "--" was present at all.
func splitClaudeArgs(args []string) (own, claude []string, given bool) {
	for i, a := range args {
		if a == "--" {
			return args[:i:i], args[i+1:], true
		}
	}
	return args, nil, false
}

// topicClaudeArgs reads a topic's stored list; nil when it has none.
func topicClaudeArgs(topicsRoot, name string) []string {
	list, _ := load(topicFile(topicsRoot, name))["claudeArgs"].([]any)
	var out []string
	for _, v := range list {
		out = append(out, scalar(v))
	}
	return out
}

// setClaudeArgs replaces the stored list; an empty one removes the key, so a topic
// without extra args has a registry entry exactly like before the feature existed.
func setClaudeArgs(topicsRoot, name string, a []string) error {
	file := topicFile(topicsRoot, name)
	return withFileLock(file, func() error {
		m := load(file)
		if len(a) == 0 {
			delete(m, "claudeArgs")
		} else {
			list := make([]any, len(a))
			for i, v := range a {
				list[i] = v
			}
			m["claudeArgs"] = list
		}
		return save(file, m)
	})
}

// launchTopic launches a topic's session with its stored claudeArgs. They go FIRST:
// topic's own arguments stay last, so a positional seed prompt is still the final
// argument claude sees. With no stored args the command line is exactly what it was.
func launchTopic(configDir, topicsRoot, name, dir string, owned ...string) error {
	return launchTopicWith(configDir, name, dir, topicClaudeArgs(topicsRoot, name), owned...)
}

// launchTopicWith launches with an explicit extra-args list. up and fork use it when
// the list is not yet in the registry: they store it only AFTER the launch, so a launch
// that fails leaves no registry entry holding nothing but claudeArgs.
func launchTopicWith(configDir, name, dir string, extra []string, owned ...string) error {
	return launch(configDir, name, dir, append(append([]string{}, extra...), owned...)...)
}

// launch is launchSession, swappable so tests can stand in for a session that stops at
// a startup prompt without needing a real claude.
var launch = launchSession

func topicLaunchArgs(topicsRoot, name string, owned []string) []string {
	return append(topicClaudeArgs(topicsRoot, name), owned...)
}

// channelSources lists the channel sources an argument list turns on
// (server:<name>, plugin:<name>), for warnings about where they must be configured.
func channelSources(a []string) []string {
	var out []string
	for i := 0; i < len(a); i++ {
		if a[i] == channelFlag {
			for i+1 < len(a) && !strings.HasPrefix(a[i+1], "-") {
				i++
				out = append(out, a[i])
			}
		} else if v, ok := strings.CutPrefix(a[i], channelFlag+"="); ok {
			out = append(out, strings.Split(v, ",")...)
		}
	}
	return out
}

func quotedArgs(a []string) string {
	q := make([]string, len(a))
	for i, v := range a {
		q[i] = shellQuote(v)
	}
	return strings.Join(q, " ")
}

// set-args <name> (-- <claude args...> | --clear) <config-dir> <topics-root>
func cmdSetArgs(args []string) error {
	usage := fmt.Errorf(`usage: topic set-args "<name>" -- <claude args...>   |   topic set-args "<name>" --clear`)
	if len(args) < 3 {
		return usage
	}
	configDir, topicsRoot := args[len(args)-2], args[len(args)-1]
	own, claude, given := splitClaudeArgs(args[:len(args)-2])
	if len(own) == 0 {
		return usage
	}
	name, clear := own[0], len(own) == 2 && own[1] == "--clear"
	switch {
	case len(own) > 2, len(own) == 2 && !clear:
		return usage
	case clear && given:
		return fmt.Errorf("set-args: --clear takes no claude args")
	case !clear && (!given || len(claude) == 0):
		return usage
	}
	if !fileExists(topicFile(topicsRoot, name)) {
		return fmt.Errorf("no such topic: %q", name)
	}
	if err := checkClaudeArgs(claude); err != nil {
		return err
	}
	if err := setClaudeArgs(topicsRoot, name, claude); err != nil {
		return err
	}
	if clear {
		fmt.Printf("%q: claude args cleared.\n", name)
	} else {
		fmt.Printf("%q: claude args: %s\n", name, quotedArgs(claude))
	}
	if isUp(profileTag(configDir), name) {
		fmt.Println("  It is up: the running session is unaffected. This applies from the next launch —")
		fmt.Printf("    topic down %q && topic up %q\n", name, name)
	}
	return nil
}

// warnChannelSources is said wherever a topic's args arrive in a profile that may not
// have the channel's MCP server configured: claude does not complain, it just drops
// the pushes.
func warnChannelSources(a []string, configDir string) {
	if src := channelSources(a); len(src) > 0 {
		fmt.Fprintf(os.Stderr, "  note: this topic turns on MCP channels (%s); each must also be\n"+
			"        configured in %s, or its pushes are silently dropped.\n",
			strings.Join(src, ", "), configDir)
	}
}
