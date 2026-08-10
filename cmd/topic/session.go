package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Orchestration: starting and stopping the Claude sessions beneath a topic.
//
// The startup heuristics here key on Claude Code's own on-screen text, which has
// changed several times. They are grouped in startupPatterns so there is one place to
// look when a launch misbehaves — suspect this first.

var startupPatterns = struct {
	trust, ready, onboarding, externalImports *regexp.Regexp
}{
	// Auto-accepted: the workspace trust prompt.
	trust: regexp.MustCompile(`trust this folder`),
	// The session is usable.
	ready: regexp.MustCompile(`(?i)Welcome back|remote-control is active`),
	// First-run setup — a theme and an account are the user's choices, not ours.
	onboarding: regexp.MustCompile(`(?i)colorblind-friendly|Select login method|Choose the text style`),
	// A security question about loading files from outside the working directory.
	externalImports: regexp.MustCompile(`Allow external CLAUDE\.md file imports`),
}

func tmuxRun(args ...string) error { return exec.Command("tmux", args...).Run() }

func capturePane(target string) string {
	out, err := exec.Command("tmux", "capture-pane", "-t", target, "-p").Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func killSession(target string) { _ = tmuxRun("kill-session", "-t", "="+target) }

// shellQuote renders an argument safe to send through tmux send-keys, which hands the
// string to a shell.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			strings.ContainsRune("@%+=:,./-_", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// launchSession creates a detached tmux session and sends the claude command into it.
// send-keys rather than `tmux new -d claude`, so the shell survives claude exiting and
// the user can relaunch in place.
func launchSession(configDir, name, dir string, claudeArgs ...string) error {
	tag := profileTag(configDir)
	tn := tmuxName(tag, name)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}

	// Pin the profile onto the command itself: a detached tmux session does NOT
	// reliably inherit our environment (the tmux server may long predate us), and a
	// session launched under the wrong profile writes its transcript somewhere this
	// tool cannot see. Only for non-default profiles — CLAUDE_CONFIG_DIR=~/.claude is
	// not the same as leaving it unset.
	cmd := "claude"
	if tag != "" {
		cmd = "CLAUDE_CONFIG_DIR=" + shellQuote(configDir) + " claude"
	}
	for _, a := range claudeArgs {
		cmd += " " + shellQuote(a)
	}

	if err := tmuxRun("new-session", "-d", "-s", tn, "-c", dir); err != nil {
		return fmt.Errorf("creating tmux session %q: %w", tn, err)
	}
	if err := tmuxRun("send-keys", "-t", tn, cmd, "Enter"); err != nil {
		return err
	}

	fmt.Print("  starting")
	trusted := false
	for range 40 {
		pane := capturePane(tn)

		// Accept the trust prompt, then KEEP POLLING: further prompts can follow it,
		// and breaking out here once reported a session up while it still sat on an
		// unanswered question.
		if !trusted && startupPatterns.trust.MatchString(pane) {
			time.Sleep(300 * time.Millisecond)
			_ = tmuxRun("send-keys", "-t", tn, "Enter")
			fmt.Print(" (trust accepted)")
			trusted = true
			continue
		}
		if startupPatterns.externalImports.MatchString(pane) {
			killSession(tn)
			fmt.Println()
			hint := "(cd " + dir + " && claude)"
			if tag != "" {
				hint = "(cd " + dir + " && CLAUDE_CONFIG_DIR=" + configDir + " claude)"
			}
			return fmt.Errorf("%q needs a one-off decision this profile has not made yet:\n"+
				"  \"Allow external CLAUDE.md file imports?\" — a security question about\n"+
				"  imports outside %s, which topic will not answer for you.\n"+
				"  Start it once by hand, answer it, then retry:\n    %s\n  unanswered startup prompt",
				name, dir, hint)
		}
		if startupPatterns.ready.MatchString(pane) {
			break
		}
		// First-run setup. Waiting is pointless and topic will not choose a theme or
		// an account, so fail fast rather than leave a session parked at a prompt
		// that isUp would happily report as UP.
		if startupPatterns.onboarding.MatchString(pane) {
			killSession(tn)
			fmt.Println()
			hint := "claude"
			if tag != "" {
				hint = "CLAUDE_CONFIG_DIR=" + configDir + " claude"
			}
			return fmt.Errorf("profile %s has not completed first-run setup.\n"+
				"  The session opened on the theme/login prompt, which topic will not answer\n"+
				"  for you. Run it once interactively, then retry:\n    %s\n"+
				"  Its config file is %s\n  profile not set up",
				configDir, hint, configFileOf(configDir))
		}
		time.Sleep(500 * time.Millisecond)
		fmt.Print(".")
	}
	fmt.Println()
	return nil
}

// stopSession quits Claude cleanly so the transcript is flushed, then removes the tmux
// session. Transcripts are written incrementally, so even a hard kill stays resumable
// — this is just the tidy path.
func stopSession(configDir, name string) {
	tn := tmuxName(profileTag(configDir), name)
	if tmuxRun("has-session", "-t", "="+tn) != nil {
		return
	}
	_ = tmuxRun("send-keys", "-t", tn, "Escape") // interrupt any in-flight turn
	time.Sleep(400 * time.Millisecond)
	_ = tmuxRun("send-keys", "-t", tn, "/exit", "Enter")
	for range 12 {
		if tmuxRun("has-session", "-t", "="+tn) != nil {
			return
		}
		out, _ := exec.Command("tmux", "list-panes", "-t", "="+tn, "-F", "#{pane_current_command}").Output()
		if !strings.Contains(strings.ToLower(string(out)), "claude") {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	killSession(tn)
}
