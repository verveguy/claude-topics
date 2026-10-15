package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTopicLaunchArgsOrdering(t *testing.T) {
	root := t.TempDir()
	home, _ := os.UserHomeDir()
	def := filepath.Join(home, ".claude")
	owned := []string{"--remote-control", "X", "--name", "X", "--session-id", "u", "seed prompt"}

	// A topic with no claudeArgs launches with the command line it always had, plus
	// the topic's name in the environment.
	const before = `env -u CLAUDE_CONFIG_DIR CLAUDE_TOPIC=X claude --remote-control X --name X --session-id u 'seed prompt'`
	if got := claudeCommand(def, "X", topicLaunchArgs(root, "X", owned)); got != before {
		t.Errorf("no-args command line changed:\n got %s\nwant %s", got, before)
	}
	// Names are free text: quoted, and pinned alongside a non-default profile.
	const named = `CLAUDE_CONFIG_DIR=/p/.claude-work CLAUDE_TOPIC='Fabrik work' claude --verbose`
	if got := claudeCommand("/p/.claude-work", "Fabrik work", []string{"--verbose"}); got != named {
		t.Errorf("command line:\n got %s\nwant %s", got, named)
	}

	// Stored args go first; the seed prompt stays the final argument.
	fabrik := []string{channelFlag, "server:fabrik"}
	if err := setClaudeArgs(root, "X", fabrik); err != nil {
		t.Fatal(err)
	}
	want := append(append([]string{}, fabrik...), owned...)
	if got := topicLaunchArgs(root, "X", owned); !reflect.DeepEqual(got, want) {
		t.Errorf("launch args = %q, want %q", got, want)
	}

	// Clearing removes the key, not just empties it.
	if err := setClaudeArgs(root, "X", nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := load(topicFile(root, "X"))["claudeArgs"]; ok {
		t.Error("cleared claudeArgs left a key behind")
	}
}

func TestCheckClaudeArgsRefusesOwnedFlags(t *testing.T) {
	for _, a := range []string{"--resume", "-r", "--resume=abc", "--session-id", "--name", "-n",
		"--remote-control", "--remote-control=Y", "--continue", "-c"} {
		if checkClaudeArgs([]string{"--verbose", a}) == nil {
			t.Errorf("%s was not refused", a)
		}
	}
	if err := checkClaudeArgs([]string{channelFlag, "server:fabrik",
		"--remote-control-session-name-prefix", "p"}); err != nil {
		t.Errorf("legitimate args refused: %v", err)
	}
}

func TestChannelSources(t *testing.T) {
	got := channelSources([]string{"--verbose", channelFlag, "server:fabrik", "plugin:x@y", "--model", "m"})
	if want := []string{"server:fabrik", "plugin:x@y"}; !reflect.DeepEqual(got, want) {
		t.Errorf("channelSources = %q, want %q", got, want)
	}
	if got := channelSources([]string{channelFlag + "=server:a,server:b"}); len(got) != 2 {
		t.Errorf("= form: %q", got)
	}
}
