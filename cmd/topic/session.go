package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Orchestration: starting and stopping the Claude sessions beneath a topic.
//
// The startup heuristics here key on Claude Code's own on-screen text, which has
// changed several times. They are grouped in startupPatterns so there is one place to
// look when a launch misbehaves — suspect this first.

var startupPatterns = struct {
	trust, trustAccept, ready, onboarding, externalImports, pendingChoice, noConversation *regexp.Regexp
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
	// The footer of any other on-screen menu, such as the one-off "Claude in Chrome
	// extension detected" question a fresh profile asks. Waiting will not clear it, and
	// what to answer is the user's call.
	pendingChoice: regexp.MustCompile(`Enter to confirm`),
	// Printed by `claude --resume` as it exits when the session has no transcript.
	noConversation: regexp.MustCompile(`No conversation found with session ID`),
}

type startupState int

const (
	startupWaiting startupState = iota
	startupTrust
	startupExternalImports
	startupOnboarding
	startupReady
	startupPendingChoice
	startupNoConversation
)

// classifyStartup reads a freshly launched pane. trusted means the trust prompt was
// already answered: its dialog can linger on screen for a moment afterwards, and must not
// then be mistaken for a question nobody has answered.
func classifyStartup(pane string, trusted bool) startupState {
	p := startupPatterns
	switch {
	case p.noConversation.MatchString(pane):
		return startupNoConversation
	case p.trust.MatchString(pane):
		if trusted {
			return startupWaiting
		}
		return startupTrust
	case p.externalImports.MatchString(pane):
		return startupExternalImports
	case p.ready.MatchString(pane):
		return startupReady
	case p.onboarding.MatchString(pane):
		return startupOnboarding
	case p.pendingChoice.MatchString(pane):
		return startupPendingChoice
	}
	return startupWaiting
}

// screenTail is the last few non-blank lines of a pane, indented for an error message.
func screenTail(pane string, n int) string {
	var lines []string
	for _, l := range strings.Split(pane, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, "    | "+strings.TrimRight(l, " "))
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
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
	out, err := exec.Command("tmux", "list-panes", "-t", "="+target, "-F", "#{pane_current_command}").Output()
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

// claudeProcessName matches Claude Code's own process: it renames itself to its version.
var claudeProcessName = regexp.MustCompile(`^(claude|\d+\.\d+\.\d+)$`)

// paneShowsClaude reports whether Claude itself is in the foreground, rather than
// anything that is merely not a shell — a fresh shell runs other commands (`gh auth
// token`, version managers) while it loads its rc files.
func paneShowsClaude(target string) bool {
	out, err := exec.Command("tmux", "list-panes", "-t", "="+target, "-F", "#{pane_current_command}").Output()
	if err != nil {
		return false
	}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if claudeProcessName.MatchString(strings.TrimSpace(l)) {
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
	if err := tmuxRun("new-session", "-d", "-s", tn, "-c", dir); err != nil {
		return fmt.Errorf("creating tmux session %q: %w", tn, err)
	}
	if err := tmuxRun("send-keys", "-t", tn, cmd, "Enter"); err != nil {
		return err
	}

	fmt.Print("  starting")
	trusted := false
	hint := "(cd " + dir + " && claude)"
	if tag != "" {
		hint = "(cd " + dir + " && CLAUDE_CONFIG_DIR=" + configDir + " claude)"
	}
	sawClaude := false
	for range 40 {
		pane := capturePane(tn)
		state := classifyStartup(pane, trusted)

		// Claude exiting during start-up leaves only the shell, which used to be reported
		// as up once the poll ran out. "Seen running" first, because a fresh shell can
		// still be loading its rc files before claude even starts.
		running := paneShowsClaude(tn)
		sawClaude = sawClaude || running
		if state == startupNoConversation || (sawClaude && !running && !paneRunningClaude(tn)) {
			fmt.Println()
			return fmt.Errorf("%q exited during start-up:\n%s\n  claude did not start", name, screenTail(capturePane(tn), 6))
		}

		// Accept the trust prompt, then KEEP POLLING: further prompts can follow it,
		// and breaking out here once reported a session up while it still sat on an
		// unanswered question.
		if state == startupTrust {
			time.Sleep(300 * time.Millisecond)
			// Re-capture: the dialog may still have been painting when we matched.
			if err := selectMenuOption(tn, capturePane(tn), startupPatterns.trustAccept); err != nil {
				killSession(tn)
				fmt.Println()
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
		if state == startupExternalImports {
			killSession(tn)
			fmt.Println()
			return fmt.Errorf("%q needs a one-off decision this profile has not made yet:\n"+
				"  \"Allow external CLAUDE.md file imports?\" — a security question about\n"+
				"  imports outside %s, which topic will not answer for you.\n"+
				"  Start it once by hand, answer it, then retry:\n    %s\n  unanswered startup prompt",
				name, dir, hint)
		}
		if state == startupReady {
			fmt.Println()
			return nil
		}
		// First-run setup. Waiting is pointless and topic will not choose a theme or
		// an account, so fail fast rather than leave a session parked at a prompt
		// that isUp would happily report as UP.
		if state == startupOnboarding {
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
		// Any other question. The session is left running, so the user can answer it
		// where it sits; it is not up until they do. Found 2026-10-04: a new profile's
		// "Claude in Chrome" question held 30 moved topics off Remote Control while topic
		// reported every one of them up.
		if state == startupPendingChoice {
			fmt.Println()
			return fmt.Errorf("%q is waiting on a question topic will not answer for you:\n%s\n"+
				"  It is still running. Answer it there and it carries on:\n    tmux attach -t %q\n"+
				"  unanswered startup prompt", name, screenTail(pane, 8), tn)
		}
		time.Sleep(500 * time.Millisecond)
		fmt.Print(".")
	}
	fmt.Println()
	// Slow is not broken — a large transcript can take a while to load — so this stays a
	// warning. But it must not pass silently as up.
	fmt.Fprintf(os.Stderr, "  note: %q has not shown it is ready after 20s. Check it:\n    tmux attach -t %q\n", name, tn)
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

// liveSessionID is the session Claude Code is actually running in a topic's tmux
// session, read from the record it keeps at <configDir>/sessions/<pid>.json. Empty when
// nothing is running there.
//
// It can differ from the registry. /clear starts a new session id inside the same
// process (verified 2026-10-02: the record follows it), and topic only records an id
// when it launches a session — so a topic that was /cleared is running a conversation
// the registry has never heard of.
func liveSessionID(configDir, name string) string {
	tn := tmuxName(profileTag(configDir), name)
	out, err := exec.Command("tmux", "list-panes", "-t", "="+tn, "-F", "#{pane_pid}").Output()
	if err != nil {
		return ""
	}
	// launchSession types the command into a shell, so Claude is the pane's child: try
	// the children first. The shell itself goes last, because session records outlive
	// their processes and pids are recycled — a shell that inherited a dead Claude's pid
	// would otherwise match that Claude's stale record ahead of the live one.
	var kids, shells []int
	for _, f := range strings.Fields(string(out)) {
		if p := atoi(f); p > 0 {
			kids = append(kids, childPids(p)...)
			shells = append(shells, p)
		}
	}
	return sessionIDOfPids(configDir, append(kids, shells...))
}

func childPids(pid int) []int {
	out, _ := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output()
	var kids []int
	for _, f := range strings.Fields(string(out)) {
		if k := atoi(f); k > 0 {
			kids = append(kids, k)
		}
	}
	return kids
}

// sessionIDOfPids returns the session id recorded for the first of pids that is alive
// and has a record. The pid inside the record must match too: a record outlives its
// process, and pids get recycled.
func sessionIDOfPids(configDir string, pids []int) string {
	for _, p := range pids {
		d := load(filepath.Join(configDir, "sessions", strconv.Itoa(p)+".json"))
		if sid := scalar(d["sessionId"]); sid != "" && atoi(scalar(d["pid"])) == p && alive(p) {
			return sid
		}
	}
	return ""
}

// liveSessionIDOf is the lookup syncSessionID uses; tests replace it, since the real
// one needs a running Claude inside tmux.
var liveSessionIDOf = liveSessionID

// syncSessionID points the registry at the session a running topic is really on. The
// id it replaces goes into history, so that conversation stays resumable and travels
// with a move like any retired session.
//
// Every command that ends, relocates or re-mints a session must call this before it
// reads the registry's sessionId. Without it they act on the conversation from before
// a /clear and strand the live one: a move carried the stale transcript across and left
// the real one behind, and the next `up` resumed the old conversation.
func syncSessionID(configDir, topicsRoot, name string, dry bool) error {
	return syncSessionIDOpt(configDir, topicsRoot, name, dry, false)
}

// syncSessionIDOpt is syncSessionID with a quiet switch for the unattended sweep, which
// would otherwise log the same "no transcript yet" line every five minutes until the
// /clear'd session gets its first message.
func syncSessionIDOpt(configDir, topicsRoot, name string, dry, quiet bool) error {
	live := liveSessionIDOf(configDir, name)
	reg := regGet(topicsRoot, name, "sessionId")
	if live == "" || live == reg {
		return nil
	}
	// A fresh /clear session may not have written its transcript yet: Claude Code
	// creates the jsonl on the first message, not at /clear. Pointing the registry at it
	// then would make the next `up` --resume a session with no conversation, which fails
	// at start-up. Keep the recorded id until the live one has a transcript to resume;
	// a later sync (every five minutes, or at down/move/rename) follows it then.
	if transcriptOf(configDir, live) == "" {
		if quiet {
			return nil
		}
		fmt.Printf("  session: %q is running %s…, but it has no transcript yet (a fresh /clear\n"+
			"    writes one on its first message) — keeping the recorded %s… for now.\n",
			name, truncate(live, 8), truncate(reg, 8))
		return nil
	}
	if dry {
		fmt.Printf("  session: %q is running %s…, not the recorded %s… (a /clear starts a new\n"+
			"    session) — the real run will follow it; this plan shows the recorded one.\n",
			name, truncate(live, 8), truncate(reg, 8))
		return nil
	}
	file := topicFile(topicsRoot, name)
	changed := false
	err := withFileLock(file, func() error {
		m := load(file)
		// Re-check under the lock: if another command changed the session since we
		// read it, that write wins and this sync stands down.
		if scalar(m["sessionId"]) != reg {
			return nil
		}
		if reg != "" {
			hist, _ := m["history"].([]any)
			m["history"] = append(hist, map[string]any{
				"sessionId":  reg,
				"replacedBy": "clear",
				"retiredAt":  time.Now().Format("2006-01-02T15:04:05-07:00"),
			})
		}
		m["sessionId"] = live
		changed = true
		return save(file, m)
	})
	if err != nil || !changed {
		return err
	}
	fmt.Printf("  session: %q is running %s…, not the recorded %s… (a /clear starts a new\n"+
		"    session) — following it; the old one is kept in history.\n",
		name, truncate(live, 8), truncate(reg, 8))
	return nil
}

// syncLiveSessions follows every running topic in a profile to the session it is really
// on. The commands that call syncSessionID only cover a topic someone acts on; a topic
// /cleared and then lost to a crash or reboot takes its live id with it. Run on the
// Dispatcher's five-minute cycle, this keeps that window to five minutes.
func syncLiveSessions(configDir, topicsRoot string) {
	for _, name := range topicNames(topicsRoot) {
		if err := syncSessionIDOpt(configDir, topicsRoot, name, false, true); err != nil {
			fmt.Fprintf(os.Stderr, "  session sync for %q failed: %v\n", name, err)
		}
	}
}

// remoteControlPatterns key on Claude Code's /remote-control UI. Like startupPatterns,
// this is the place to look first if Claude Code changes its wording.
var remoteControlPatterns = struct {
	connected, disconnectOption, disconnected *regexp.Regexp
}{
	// The slash-command autocomplete describes /remote-control by what it would do.
	// It reads "Disconnect Remote Control" only while a bridge is connected; otherwise
	// the command CONNECTS one, so nothing may be confirmed without seeing this.
	connected: regexp.MustCompile(`/remote-control\s+Disconnect Remote Control`),
	// The option in the dialog that opens on Enter.
	disconnectOption: regexp.MustCompile(`Disconnect this session`),
	// Printed once Claude Code has ended the bridge.
	disconnected: regexp.MustCompile(`Remote Control disconnected`),
}

// cloudSessionURL is where a bridge's cloud session appears in the Claude UI.
func cloudSessionURL(bridgeID string) string {
	return "https://claude.ai/code/session_" + strings.TrimPrefix(bridgeID, "cse_")
}

// disconnectRemoteControl ends a running topic's Remote Control session from inside
// Claude Code, which archives the cloud session under the account that owns it.
//
// It exists because a plain `down` does not do this. Claude Code keeps the cloud
// session for resume on /exit and only marks it offline, and once move, rename or
// rebridge strip the bridge record from the transcript, nothing ever reconnects to it:
// the old account is left listing a dead session under the topic's name, and every
// re-mint adds another. Disconnecting first lets Claude Code clean up with its own
// credentials, while it is still running as the source account.
//
// Returns true once Claude Code confirms the disconnect. A false return with a nil
// error means there was nothing to end: the topic is not running, or has no bridge.
func disconnectRemoteControl(configDir, name string) (bool, error) {
	tn := tmuxName(profileTag(configDir), name)
	if !sessionExists(tn) || !paneRunningClaude(tn) {
		return false, nil
	}
	clearInput := func() { _ = tmuxRun("send-keys", "-t", tn, "C-u") }

	_ = tmuxRun("send-keys", "-t", tn, "Escape") // interrupt any in-flight turn
	time.Sleep(400 * time.Millisecond)
	clearInput()
	_ = tmuxRun("send-keys", "-t", tn, "-l", "/remote-control")
	time.Sleep(1200 * time.Millisecond)
	if !remoteControlPatterns.connected.MatchString(capturePane(tn)) {
		clearInput()
		return false, nil
	}

	_ = tmuxRun("send-keys", "-t", tn, "Enter")
	var pane string
	for range 20 {
		time.Sleep(250 * time.Millisecond)
		if pane = capturePane(tn); remoteControlPatterns.disconnectOption.MatchString(pane) {
			break
		}
	}
	if err := selectMenuOption(tn, pane, remoteControlPatterns.disconnectOption); err != nil {
		_ = tmuxRun("send-keys", "-t", tn, "Escape")
		return false, err
	}
	for range 40 {
		time.Sleep(250 * time.Millisecond)
		if remoteControlPatterns.disconnected.MatchString(capturePane(tn)) {
			return true, nil
		}
	}
	return false, fmt.Errorf("Claude Code did not confirm the disconnect")
}

// retireRemoteControl is what move, rename and rebridge call before they discard a
// topic's bridge record. It never blocks the operation: failing to archive leaves an
// orphan, which is exactly what happened before this existed, so it says where the
// orphan is instead of stopping.
func retireRemoteControl(configDir, name, transcript string, wasUp bool) {
	_, bridge := lastBridgeOf(transcript)
	if bridge == "" {
		return // no bridge, or already disconnected
	}
	if wasUp {
		ok, err := disconnectRemoteControl(configDir, name)
		if ok {
			fmt.Printf("  remote control: disconnected %s… — its cloud session is archived\n", truncate(bridge, 16))
			return
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "  remote control: could not disconnect %q (%v)\n", name, err)
		}
	}
	reason := "it is down, so it cannot end its cloud session from inside"
	if wasUp {
		reason = "it did not disconnect"
	}
	fmt.Fprintf(os.Stderr, "  remote control: %s — the old cloud session stays listed in the\n", reason)
	fmt.Fprintf(os.Stderr, "    source account. Archive it there: %s\n", cloudSessionURL(bridge))
}
