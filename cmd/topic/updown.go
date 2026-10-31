package main

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// up and down: the two verbs the whole tool exists for. Picking a topic up resumes its
// stored session UUID, so it is a genuine --resume; putting it down terminates the
// process but keeps that UUID, which is what makes put-down lossless.

func nowISO() string { return time.Now().Format("2006-01-02T15:04:05-0700") }

// newUUID mints a session id. Format matters only in that Claude Code accepts it.
func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// lockAcquire keeps a topic a singleton: two callers must not race it up. A lock older
// than five minutes is assumed stale and reclaimed.
func lockAcquire(topicsRoot, name string) (release func(), err error) {
	dir := filepath.Join(topicsRoot, ".locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, slugify(name)+".lock")
	if err := os.Mkdir(path, 0o755); err != nil {
		fi, statErr := os.Stat(path)
		if statErr == nil && time.Since(fi.ModTime()) > 5*time.Minute {
			_ = os.Remove(path)
			if err2 := os.Mkdir(path, 0o755); err2 != nil {
				return nil, fmt.Errorf("could not acquire lock for %q", name)
			}
		} else {
			return nil, fmt.Errorf("%q is being started by another process (lock held)", name)
		}
	}
	return func() { _ = os.Remove(path) }, nil
}

// liveSessionRecord reports whether this profile has a LIVE session with both that pid
// and that session id — matching on both, in case a pid has been recycled.
func liveSessionRecord(configDir, pid, sessionID string) bool {
	paths, _ := filepath.Glob(filepath.Join(configDir, "sessions", "*.json"))
	for _, p := range paths {
		d := load(p)
		if scalar(d["pid"]) != pid || scalar(d["sessionId"]) != sessionID {
			continue
		}
		if n := atoi(pid); n > 0 && alive(n) {
			return true
		}
	}
	return false
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

//	up <name> [dir] <config-dir> <topics-root> [--claim] [--seed-from <file>]
//
// The trailing config-dir and topics-root are supplied by the front-end, so this stays
// a pure function of its arguments rather than of the environment.
func cmdUp(args []string) error {
	claim, seedFrom := false, ""
	var positional []string
	// The profile is always appended last; everything after a "--" before it is
	// claude's, to be stored as the topic's claudeArgs.
	var userArgs []string
	if len(args) >= 2 {
		var own []string
		own, userArgs, _ = splitClaudeArgs(args[:len(args)-2])
		args = append(own, args[len(args)-2:]...)
	}
	if err := checkClaudeArgs(userArgs); err != nil {
		return err
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--claim":
			claim = true
		case "--seed-from":
			if i+1 >= len(args) {
				return fmt.Errorf("--seed-from needs a file")
			}
			i++
			seedFrom = args[i]
		default:
			// An unrecognised flag is a mistake, not a topic name: taking it as one
			// creates a topic called "--verbose".
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unknown flag for up: %s (try: topic up --help)", args[i])
			}
			positional = append(positional, args[i])
		}
	}
	if len(positional) < 3 {
		return fmt.Errorf(`usage: topic up "<name>" [dir] [--claim] [--seed-from <file>] [-- <claude args...>]`)
	}
	// name [dir] config-dir topics-root
	name := positional[0]
	configDir, topicsRoot := positional[len(positional)-2], positional[len(positional)-1]
	dir := ""
	if len(positional) == 4 {
		dir = positional[1]
	}

	if seedFrom != "" {
		fi, err := os.Stat(seedFrom)
		if err != nil {
			return fmt.Errorf("--seed-from: no such file: %s", seedFrom)
		}
		if fi.Size() == 0 {
			return fmt.Errorf("--seed-from: file is empty: %s", seedFrom)
		}
		if abs, err := filepath.Abs(seedFrom); err == nil {
			seedFrom = abs
		}
	}

	release, err := lockAcquire(topicsRoot, name)
	if err != nil {
		return err
	}
	defer release()

	tag := profileTag(configDir)
	file := topicFile(topicsRoot, name)

	// An adopted topic still has its ORIGINAL process running outside tmux. Starting
	// here would --resume the same UUID in a second process: two writers on one
	// transcript, which corrupts it.
	if regGet(topicsRoot, name, "state") == "adopted" {
		adoptedPid := regGet(topicsRoot, name, "adoptedPid")
		sid := regGet(topicsRoot, name, "sessionId")
		if adoptedPid != "" {
			// The pid is on record, so the hazard IS detectable — and --claim must
			// not override a process we can still see.
			if liveSessionRecord(configDir, adoptedPid, sid) {
				fmt.Fprintf(os.Stderr, "%q is still running as pid %s, outside tmux.\n", name, adoptedPid)
				fmt.Fprintf(os.Stderr, "  Resuming session %s… in a second process would corrupt its\n", truncate(sid, 8))
				fmt.Fprintln(os.Stderr, "  transcript. Exit that session (/exit in its window), then re-run.")
				return fmt.Errorf("refusing to start while the original process is alive")
			}
			claim = true // positively verified gone; no assertion needed
		}
		if !claim {
			fmt.Fprintf(os.Stderr, "%q was adopted but not yet claimed.\n\n", name)
			fmt.Fprintln(os.Stderr, "  Its original session is running OUTSIDE tmux. Starting it here would")
			fmt.Fprintln(os.Stderr, "  resume the same session UUID in a second process and corrupt the")
			fmt.Fprintln(os.Stderr, "  transcript.")
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "  1. Exit the original session (/exit in its window).")
			fmt.Fprintf(os.Stderr, "  2. topic up %q --claim\n", name)
			return fmt.Errorf("refusing to start until the original session has exited")
		}
	}

	if isUp(tag, name) {
		fmt.Printf("%q is already up.\n", name)
		fmt.Printf("  attach: %s\n", attachCommand(tmuxName(tag, name)))
		// Only for a topic with a registry entry: writing one here would create an entry
		// holding nothing but claudeArgs.
		if len(userArgs) > 0 && fileExists(file) {
			if err := setClaudeArgs(topicsRoot, name, userArgs); err != nil {
				return err
			}
			fmt.Printf("  claude args recorded (%s) — they apply from the next launch;\n", quotedArgs(userArgs))
			fmt.Println("  the running session is unaffected.")
		}
		return nil
	}

	// Given args replace the stored list; otherwise the stored list is used. They are
	// stored only once the launch has produced a session (see recordLaunch below).
	extra := userArgs
	if len(extra) == 0 {
		extra = topicClaudeArgs(topicsRoot, name)
	}
	storeArgs := func() error {
		if len(userArgs) == 0 {
			return nil
		}
		return setClaudeArgs(topicsRoot, name, userArgs)
	}

	if dir == "" {
		dir = regGet(topicsRoot, name, "dir")
	}
	if dir == "" {
		dir, _ = os.Getwd()
	}
	sid := regGet(topicsRoot, name, "sessionId")

	if sid != "" {
		if seedFrom != "" {
			fmt.Fprintf(os.Stderr, "note: --seed-from ignored; %q has an existing session to resume.\n", name)
		}
		fmt.Printf("Picking up %q (resuming session %s…) in %s\n", name, truncate(sid, 8), dir)
		if err := recordLaunch(launchTopicWith(configDir, name, dir, extra,
			"--remote-control", name, "--name", name, "--resume", sid), storeArgs); err != nil {
			return err
		}
	} else {
		sid = newUUID()
		fmt.Printf("Starting %q (new session %s…) in %s\n", name, truncate(sid, 8), dir)
		launchArgs := []string{"--remote-control", name, "--name", name, "--session-id", sid}
		if seedFrom != "" {
			// The same seeding path handoff-swap uses, but for a brand-new topic.
			launchArgs = append(launchArgs, fmt.Sprintf(
				"You are starting the topic %q. Read the briefing document at %s — it contains "+
					"the context you need. Summarise briefly what you picked up, then continue from there.",
				name, seedFrom))
		}
		launchErr := launchTopicWith(configDir, name, dir, extra, launchArgs...)
		// A session parked at a startup question is still running and still this
		// topic's: register it, or `topic down` cannot find it and the next `up` starts
		// a different session, in whatever directory the shell happens to be in.
		if err := recordLaunch(launchErr, func() error {
			fields := []string{"name", name, "generation", "1", "dir", dir, "sessionId", sid,
				"state", "up", "lastPickedUp", nowISO()}
			if seedFrom != "" {
				fields = append(fields, "seededFrom", seedFrom)
			}
			if err := cmdSet(append([]string{file}, fields...)); err != nil {
				return err
			}
			return storeArgs()
		}); err != nil {
			return err
		}
	}

	if err := cmdSet([]string{file, "name", name, "dir", dir, "sessionId", sid,
		"state", "up", "lastPickedUp", nowISO()}); err != nil {
		return err
	}
	fmt.Println(upMessage(configDir, name))
	return nil
}

// down <name> <config-dir> <topics-root>
func cmdDown(args []string) error {
	need(args, 3, `down "<name>" <config-dir> <topics-root>`)
	name, configDir, topicsRoot := args[0], args[1], args[2]

	if !isUp(profileTag(configDir), name) {
		// An adopted session runs outside tmux, so we cannot stop it for the user.
		// Say so plainly rather than reporting a false "already down".
		if regGet(topicsRoot, name, "state") == "adopted" {
			fmt.Printf("%q was adopted and is running OUTSIDE tmux — topic cannot stop it.\n", name)
			fmt.Println("  Exit that session yourself (/exit in its window), then:")
			fmt.Printf("    topic up %q   # relaunches it under topic management, resuming context\n", name)
			return nil
		}
		// A husk — the tmux session outlived the Claude process inside it. Clear it so
		// the name is free and `topic list` stops showing a window that does nothing.
		if clearHusk(profileTag(configDir), name) {
			fmt.Printf("%q had already exited; cleared its leftover tmux session.\n", name)
			return cmdSet([]string{topicFile(topicsRoot, name), "state", "down", "lastPutDown", nowISO()})
		}
		fmt.Printf("%q is already down.\n", name)
		return nil
	}
	fmt.Printf("Putting down %q…\n", name)
	// Last chance: once the process exits, the record of which session it was on goes
	// with it, and the next `up` would resume whatever the registry last heard of.
	if err := syncSessionID(configDir, topicsRoot, name, false); err != nil {
		return err
	}
	stopSession(configDir, name)
	if err := cmdSet([]string{topicFile(topicsRoot, name), "state", "down", "lastPutDown", nowISO()}); err != nil {
		return err
	}
	fmt.Printf("%q is down. Session retained — `topic up %q` resumes it.\n", name, name)
	return nil
}

// down-all <config-dir> <topics-root> [--include-self] [--dry-run]
func cmdDownAll(args []string) error {
	includeSelf, dry := false, false
	var positional []string
	for _, a := range args {
		switch a {
		case "--include-self":
			includeSelf = true
		case "--dry-run":
			dry = true
		default:
			positional = append(positional, a)
		}
	}
	need(positional, 2, "down-all <config-dir> <topics-root>")
	configDir, topicsRoot := positional[0], positional[1]
	tag := profileTag(configDir)
	self := whoami(topicsRoot, configDir)

	did, skipped := false, ""
	for _, name := range topicNames(topicsRoot) {
		if !isUp(tag, name) {
			// down-all is the bulk cleanup path — topics-cycle down runs it — so it
			// has to clear husks too, or a crashed session survives a full cycle.
			if hasHusk(tag, name) {
				did = true
				if dry {
					fmt.Printf("  would clear leftover session: %s\n", name)
					continue
				}
				clearHusk(tag, name)
				fmt.Printf("  %q had already exited; cleared its leftover tmux session.\n", name)
				if err := cmdSet([]string{topicFile(topicsRoot, name), "state", "down", "lastPutDown", nowISO()}); err != nil {
					return err
				}
			}
			continue
		}
		// Putting our own topic down terminates the session running this command, so
		// the rest of the loop would never execute.
		if name == self && !includeSelf {
			skipped = name
			continue
		}
		did = true
		if dry {
			fmt.Printf("  would put down: %s\n", name)
			// Report a /clear drift the real run would follow; dry, so nothing is written.
			if err := syncSessionID(configDir, topicsRoot, name, true); err != nil {
				return err
			}
			continue
		}
		if err := cmdDown([]string{name, configDir, topicsRoot}); err != nil {
			return err
		}
	}
	if !did {
		fmt.Println("  nothing up in this profile.")
	}
	if skipped != "" {
		fmt.Println()
		fmt.Printf("  Skipped %q — it is the session running this command.\n", skipped)
		fmt.Printf("  Put it down last, yourself:  topic down %q\n", skipped)
	}
	return nil
}

// whoami <config-dir> <topics-root>
func cmdWhoami(args []string) error {
	need(args, 2, "whoami <config-dir> <topics-root>")
	configDir, topicsRoot := args[0], args[1]
	if os.Getenv("TMUX") == "" {
		return fmt.Errorf("whoami: not inside tmux — no topic to infer")
	}
	if name := whoami(topicsRoot, configDir); name != "" {
		fmt.Println(name)
		return nil
	}
	return fmt.Errorf("whoami: this tmux session is not a registered topic")
}

// path <kind> <name> <topics-root>   (kind: handoffs | briefs)
func cmdPath(args []string) error {
	need(args, 3, "path <handoffs|briefs> <name> <topics-root>")
	kind, name, topicsRoot := args[0], args[1], args[2]
	if kind != "handoffs" && kind != "briefs" {
		return fmt.Errorf("path: kind must be handoffs or briefs")
	}
	dir := filepath.Join(topicDir(topicsRoot, name), kind)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	fmt.Println(filepath.Join(dir, time.Now().Format("20060102-150405")+".md"))
	return nil
}

// ensureDispatcher starts the profile's Dispatcher if it is not already up. Run by
// launchd at login and every five minutes, so it must be idempotent.
//
//	ensure-dispatcher <name> <config-dir> <topics-root>
func cmdEnsureDispatcher(args []string) error {
	need(args, 3, "ensure-dispatcher <name> <config-dir> <topics-root>")
	name, configDir, topicsRoot := args[0], args[1], args[2]
	// launchd already runs this every five minutes, so it doubles as the sweep that keeps
	// the registry on each live topic's real session.
	defer syncLiveSessions(configDir, topicsRoot)
	// And it is the keeper for the profile's daemons (Fabrik, Pruefer): the same
	// five-minute cycle restarts any that are down. See daemons.go.
	defer ensureDaemons(configDir)
	if isUp(profileTag(configDir), name) {
		// Up is not the same as working: a Dispatcher whose profile lost its sign-in sits
		// at its prompt with Remote Control off. Say so on every sweep, and fail, so the
		// log and the scheduler's last result both show it.
		if tn := tmuxName(profileTag(configDir), name); startupPatterns.signedOut.MatchString(capturePane(tn)) {
			return signedOutError(name, configDir, tn)
		}
		fmt.Printf("%q is up.\n", name)
		return nil
	}
	dir := regGet(topicsRoot, name, "dir")
	if dir == "" {
		dir = dispatcherDir(configDir)
	}
	fmt.Printf("%q is down — starting it.\n", name)
	return cmdUp([]string{name, dir, configDir, topicsRoot})
}

// dispatcherDir is where a Dispatcher session runs when the registry records no
// directory for it.
//
// Deliberately NOT $HOME, which is what this was until 2026-08-31. The Dispatcher is a
// long-lived Claude session whose whole job is running `topic` commands for the other
// topics; it does no file work of its own. But a session sitting in the home directory
// makes an unqualified search mean "walk everything you own", and on macOS that trips a
// TCC prompt for every protected folder it reaches — Photos, Documents, Desktop,
// Downloads, other apps' data. Each one is attributed to `topic`, because topic is the
// responsible process for the whole tmux tree (see signing.go), so the dialogs accuse
// topic of wanting your photo library. Parking the Dispatcher somewhere inert removes
// the default that makes that traversal the path of least resistance.
//
// It is not a sandbox: an absolute path still goes where it is pointed. It removes a
// default, not a capability.
//
// This does NOT constrain the topics the Dispatcher starts. `topic up` takes its
// directory from its argument or the registry and hands it to tmux as -c, so every
// other topic starts exactly where it always did.
func dispatcherDir(configDir string) string {
	if d := os.Getenv("CLAUDE_DISPATCHER_DIR"); d != "" {
		return d
	}
	d := filepath.Join(configDir, "dispatcher")
	err := os.MkdirAll(d, 0o700)
	if err == nil {
		return d
	}
	// A Dispatcher in the wrong directory still beats no Dispatcher: without one there
	// is no way to bring topics up remotely, which is the whole point of it. But not
	// $HOME — a search from there is the bug this directory exists to prevent — so fall
	// back to a private temp directory, which macOS does not guard with TCC prompts.
	// And say so: this runs unattended under launchd, where a silent fallback would go
	// unnoticed until the permission dialogs came back.
	tag := profileTag(configDir)
	if tag == "" {
		tag = "default"
	}
	fallback := filepath.Join(os.TempDir(), "topic-dispatcher-"+tag)
	if ferr := os.MkdirAll(fallback, 0o700); ferr == nil {
		fmt.Fprintf(os.Stderr, "[warn] could not create %s (%v) — running the Dispatcher in %s instead\n", d, err, fallback)
		return fallback
	}
	home, _ := os.UserHomeDir()
	fmt.Fprintf(os.Stderr, "[warn] could not create %s (%v) or %s — running the Dispatcher in $HOME;\n", d, err, fallback)
	fmt.Fprintln(os.Stderr, "       unqualified searches from it will raise macOS permission dialogs naming `topic`")
	return home
}
