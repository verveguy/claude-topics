package main

import (
	"strings"
	"testing"
)

// Screens as captured from real launches on 2026-10-04.
const (
	chromeQuestion = `  Claude in Chrome extension detected
  Claude will use your Chrome browser by default — navigating sites, filling
  forms, and capturing screenshots in your existing session.
  ❯ No, keep browser tools off
    Yes, use my browser
  Enter to confirm · Esc to keep browser tools off`

	noConversation = `bpja@bretts liminis-framework % CLAUDE_CONFIG_DIR=/Users/bpja/.claude-kmba claude --resume dd77a3d8
No conversation found with session ID: dd77a3d8-24dc-4dcc-aed8-dc70cf720adf
bpja@bretts liminis-framework %`

	trustQuestion = `  Do you trust the files in this folder? Is this a project you trust this folder
  ❯ 1. Yes, I trust this folder
    2. No, exit
  Enter to confirm · Esc to cancel`

	// As captured from `topic up -- --dangerously-load-development-channels server:...`
	// on Claude Code v2.1.300, 2026-10-15.
	devChannelsQuestion = `  WARNING: Loading development channels

  --dangerously-load-development-channels is for local channel development
  only. Do not use this option to run channels you have downloaded off the
  internet.

  Please use --channels to run a list of approved channels.

  Channels: server:fabrik

  ❯ 1. I am using this for local development
    2. Exit

  Enter to confirm · Esc to cancel`

	readyScreen = `  /remote-control is active · Continue here, on your phone, or at claude.ai/code`

	// A Claude Enterprise account whose organization disables Remote Control, as
	// captured from the Dispatcher on Claude Code v2.1.302, 2026-10-18. No banner ever
	// appears, so this used to run out the 20s poll on every launch.
	remoteControlDisabledScreen = ` ▐▛███▛█   Claude Code v2.1.302
▝▜██████▀  Sonnet 5.5 with medium effort · Claude Enterprise
 ▝▝   ▝▝   ~/.claude/dispatcher
⏺ Remote Control is disabled by your organization's policy. Contact your
  organization admin for access.
─────────────────────────────────────────────────────────────────── Dispatcher ─
❯ Try "create a util logging.py that..."`
)

func TestClassifyStartup(t *testing.T) {
	cases := []struct {
		name    string
		pane    string
		trusted bool
		want    startupState
	}{
		// The bug: this sat on screen while topic reported the session up.
		{"chrome question is a pending choice", chromeQuestion, false, startupPendingChoice},
		{"no transcript to resume", noConversation, false, startupNoConversation},
		{"trust prompt is answered by topic", trustQuestion, false, startupTrust},
		// After accepting, the dialog can linger a moment; it is not a new question.
		{"lingering trust dialog after accepting", trustQuestion, true, startupWaiting},
		{"dev channels confirmation", devChannelsQuestion, false, startupDevChannels},
		{"ready", readyScreen, false, startupReady},
		{"ready without remote control", remoteControlDisabledScreen, false, startupReady},
		{"still loading", "bpja@bretts dev % CLAUDE_CONFIG_DIR=x claude --resume abc", false, startupWaiting},
	}
	for _, c := range cases {
		if got := classifyStartup(c.pane, c.trusted); got != c.want {
			t.Errorf("%s: classifyStartup = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestPaneCommandName(t *testing.T) {
	for in, want := range map[string]string{
		"claude":  "claude",
		"2.1.300": "2.1.300",
		"-zsh":    "zsh",
		"bash":    "bash",
		// Windows: a native program arrives as its full, escaped Windows path.
		`C:\\Users\\bpjad\\.local\\bin\\claude.exe`: "claude",
		`C:\Users\bpjad\.local\bin\Claude.EXE`:      "claude",
		"/usr/bin/bash":                             "bash",
	} {
		if got := paneCommandName(in); got != want {
			t.Errorf("paneCommandName(%q) = %q, want %q", in, got, want)
		}
		if want == "claude" && !claudeProcessName.MatchString(paneCommandName(in)) {
			t.Errorf("%q should count as Claude", in)
		}
	}
}

func TestClaudeProcessName(t *testing.T) {
	for _, n := range []string{"2.1.289", "claude"} {
		if !claudeProcessName.MatchString(n) {
			t.Errorf("%q should count as Claude", n)
		}
	}
	// What a fresh shell runs while loading its rc files must not.
	for _, n := range []string{"zsh", "gh", "node", "git"} {
		if claudeProcessName.MatchString(n) {
			t.Errorf("%q must not count as Claude", n)
		}
	}
}

func TestScreenTailKeepsTheLastNonBlankLines(t *testing.T) {
	got := screenTail("a\n\n b \nc\n\n", 2)
	if want := "    |  b\n    | c"; got != want {
		t.Errorf("screenTail = %q, want %q", got, want)
	}
	if strings.Count(screenTail(chromeQuestion, 8), "\n") != 5 {
		t.Errorf("screenTail should keep all six lines of a short screen")
	}
}

// topic confirms the development-channels screen only for what the user stored: the
// flag in the topic's claudeArgs, and exactly the sources it names.
func TestDevChannelsMatch(t *testing.T) {
	flag := "--dangerously-load-development-channels"
	multi := strings.Replace(devChannelsQuestion, "server:fabrik", "server:fabrik, plugin:x@y", 1)
	cases := []struct {
		name string
		pane string
		args []string
		want bool
	}{
		{"the stored source", devChannelsQuestion, []string{flag, "server:fabrik"}, true},
		{"= form", devChannelsQuestion, []string{flag + "=server:fabrik"}, true},
		{"no stored args: not ours to answer", devChannelsQuestion, nil, false},
		{"stored flag lists another source", devChannelsQuestion, []string{flag, "server:other"}, false},
		{"screen lists more than was stored", multi, []string{flag, "server:fabrik"}, false},
		{"screen lists fewer than was stored", devChannelsQuestion, []string{flag, "server:fabrik", "plugin:x@y"}, false},
		{"both sources stored", multi, []string{flag, "server:fabrik", "plugin:x@y"}, true},
		{"--channels is not the dev flag", devChannelsQuestion, []string{"--channels", "server:fabrik"}, false},
		{"no channels line on screen", "I am using this for local development", []string{flag, "server:fabrik"}, false},
	}
	for _, c := range cases {
		if got := devChannelsMatch(c.pane, c.args); got != c.want {
			t.Errorf("%s: devChannelsMatch = %v, want %v", c.name, got, c.want)
		}
	}
}
