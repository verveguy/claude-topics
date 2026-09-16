package main

import (
	"fmt"
	"os"
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
	trust, trustAccept, ready, onboarding, externalImports *regexp.Regexp
}{
	// Auto-accepted: the workspace trust prompt.
	trust: regexp.MustCompile(`trust this folder`),
	// The option to select once it appears. It is NOT reliably the default one: when
	// the folder pre-approves tool permissions in settings.json, Claude Code shows a
	// sterner variant of the dialog whose pre-selected option is "No, exit". Pressing
	// Enter blind — which this code used to do — then declines and quits.
	trustAccept: regexp.MustCompile(`(?i)Yes, I trust|Yes, proceed`),
	// The session is usable.
	ready: regexp.MustCompile(`(?i)Welcome back|remote-control is active`),
	// First-run setup — a theme and an account are the user's choices, not ours.
	onboarding: regexp.MustCompile(`(?i)colorblind-friendly|Select login method|Choose the text style`),
	// A security question about loading files from outside the working directory.
	externalImports: regexp.MustCompile(`Allow external CLAUDE\.md file imports`),
}

func tmuxRun(args ...string) error { return tmuxCmd(args...).Run() }

func capturePane(target string) string {
	out, err := tmuxCmd("capture-pane", "-t", target, "-p").Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func killSession(target string) { _ = tmuxRun("kill-session", "-t", "="+target) }

func sessionExists(target string) bool { return tmuxRun("has-session", "-t", "="+target) == nil }

// hasHusk reports a topic whose tmux session outlived the Claude process inside it.
// isUp calls a husk down, so every command that acts on "a topic that is not up" has
// to reckon with one: the husk still holds the tmux name, and once its registry entry
// is gone nothing can name it to clean it up.
func hasHusk(tag, name string) bool {
	tn := tmuxName(tag, name)
	return sessionExists(tn) && !paneRunningClaude(tn)
}

// clearHusk removes that leftover session. It is an empty shell, so nothing is lost.
func clearHusk(tag, name string) bool {
	if !hasHusk(tag, name) {
		return false
	}
	killSession(tmuxName(tag, name))
	return true
}

// Claude Code renames its own process to its version string ("2.1.251"), so a pane
// running Claude cannot be recognised by looking for "claude" in the command name.
// Recognise the opposite instead: a pane whose foreground command is a shell has
// nothing running in it.
var shellCommands = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true,
	"ksh": true, "csh": true, "tcsh": true, "dash": true,
}

// paneRunningClaude reports whether anything is still running inside the session's
// panes. launchSession deliberately leaves the shell alive when Claude exits, so this
// is what separates a live topic from the husk left behind by one that quit.
func paneRunningClaude(target string) bool {
	out, err := tmuxCmd("list-panes", "-t", "="+target, "-F", "#{pane_current_command}").Output()
	if err != nil {
		return false
	}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		c := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(l), "-"))
		if c != "" && !shellCommands[c] {
			return true
		}
	}
	return false
}

// menuCursorPrefixes are the glyphs Claude Code has used to mark the selected line of
// an on-screen menu.
var menuCursorPrefixes = []string{"\u276f", "\u203a", ">"}

func isMenuCursorLine(l string) bool {
	t := strings.TrimSpace(l)
	for _, p := range menuCursorPrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// selectMenuOption moves the cursor of an on-screen menu onto the line matching want
// and confirms it, rather than trusting want to be the default selection. It verifies
// the cursor actually landed before pressing Enter: on this particular dialog the
// wrong line means "No, exit", so a blind confirm is worse than giving up.
func selectMenuOption(target, pane string, want *regexp.Regexp) error {
	find := func(pane string) (cursor, wanted int) {
		cursor, wanted = -1, -1
		lines := strings.Split(pane, "\n")
		for i, l := range lines {
			if want.MatchString(l) {
				wanted = i
			}
		}
		if wanted < 0 {
			return -1, -1
		}
		// A ready session paints its own input prompt with the same glyph, so prefer
		// the cursor nearest the option we are aiming at.
		for i, l := range lines {
			if !isMenuCursorLine(l) {
				continue
			}
			if cursor < 0 || abs(i-wanted) < abs(cursor-wanted) {
				cursor = i
			}
		}
		return cursor, wanted
	}

	cursor, wanted := find(pane)
	if wanted < 0 {
		return fmt.Errorf("no option matching %s on screen", want)
	}
	if cursor < 0 {
		return fmt.Errorf("could not find the menu cursor on screen")
	}

	key, steps := "Down", wanted-cursor
	if steps < 0 {
		key, steps = "Up", -steps
	}
	for range steps {
		_ = tmuxRun("send-keys", "-t", target, key)
		time.Sleep(120 * time.Millisecond)
	}
	time.Sleep(250 * time.Millisecond)

	// Confirm the cursor is where we think it is before committing.
	pane = capturePane(target)
	cursor, wanted = find(pane)
	if wanted < 0 || cursor != wanted {
		return fmt.Errorf("could not move the selection onto %s", want)
	}
	return tmuxRun("send-keys", "-t", target, "Enter")
}

func abs(i int) int {
	if i < 0 {
		return -i
	}
	return i
}

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
	// tool cannot see. The default profile is pinned too, by UNSETTING the variable:
	// "default" means absent, not ~/.claude, and the tmux server's own environment may
	// carry a stale CLAUDE_CONFIG_DIR from whichever profile happened to start it.
	cmd := "env -u CLAUDE_CONFIG_DIR claude"
	if tag != "" {
		cmd = "CLAUDE_CONFIG_DIR=" + shellQuote(configDir) + " claude"
	}
	for _, a := range claudeArgs {
		cmd += " " + shellQuote(a)
	}

	// We may be relaunching over the husk of a session that quit — tmux refuses to
	// reuse the name. Kill outright rather than clearHusk: isUp said this topic was
	// down, so anything still in the pane is not a session we mean to keep.
	if sessionExists(tn) {
		killSession(tn)
	}
	if err := tmuxStartSession(append([]string{"new-session", "-d", "-s", tn, "-c", tmuxDir(dir)}, tmuxShell()...)...); err != nil {
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
			// Re-capture: the dialog may still have been painting when we matched.
			if err := selectMenuOption(tn, capturePane(tn), startupPatterns.trustAccept); err != nil {
				killSession(tn)
				fmt.Println()
				hint := "(cd " + dir + " && claude)"
				if tag != "" {
					hint = "(cd " + dir + " && CLAUDE_CONFIG_DIR=" + configDir + " claude)"
				}
				return fmt.Errorf("%q stopped on the workspace trust prompt and topic could not\n"+
					"  answer it safely: %v.\n"+
					"  Declining that prompt quits Claude, so topic will not guess. Start it once\n"+
					"  by hand, answer it, then retry:\n    %s\n  unanswered startup prompt",
					name, err, hint)
			}
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
	if !sessionExists(tn) {
		return
	}
	_ = tmuxRun("send-keys", "-t", tn, "Escape") // interrupt any in-flight turn
	time.Sleep(400 * time.Millisecond)
	_ = tmuxRun("send-keys", "-t", tn, "/exit", "Enter")
	for range 12 {
		if tmuxRun("has-session", "-t", "="+tn) != nil {
			return
		}
		if !paneRunningClaude(tn) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	killSession(tn)
}
