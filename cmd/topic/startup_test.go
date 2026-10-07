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

	readyScreen = `  /remote-control is active · Continue here, on your phone, or at claude.ai/code`
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
		{"ready", readyScreen, false, startupReady},
		{"still loading", "bpja@bretts dev % CLAUDE_CONFIG_DIR=x claude --resume abc", false, startupWaiting},
	}
	for _, c := range cases {
		if got := classifyStartup(c.pane, c.trusted); got != c.want {
			t.Errorf("%s: classifyStartup = %d, want %d", c.name, got, c.want)
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
