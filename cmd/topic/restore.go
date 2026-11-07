package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// restore: bring back the topics a reboot, a crash or `topic cycle down` took down.
//
// It works from the registry's own state, kept accurate as sessions come and go:
//
//	up      written by every launch
//	down    written by `topic down`, a deliberate put-down
//	exited  written by the session itself when Claude quits on its own — /exit, a
//	        crash of Claude alone — through a hook chained after `claude` on the line
//	        typed into its pane (see exitHook)
//
// A host crash, a reboot or `tmux kill-server` kills the pane's shell before that hook
// can run, so those topics keep "up" while nothing runs them. That pair — registry up,
// session not running — is exactly "died with the machine", and it is what restore
// brings back. Without the hook the registry could not tell the two apart: on one
// machine (2026-11-07) 80 topics said up and 56 were running.
//
// `topic cycle down` puts topics down deliberately, so it flags each one it takes
// (restoreKey), and `cycle up` restores those too.

// restoreKey marks a topic `topic cycle down` put down, for `cycle up` to bring back.
const restoreKey = "restoreOnCycleUp"

// exitHook is chained after the claude command typed into a topic's pane: it runs only
// when Claude exits by itself, and records that the topic is no longer up. Verified
// 2026-11-07 (zsh, tmux 3.8): a chained command runs after a normal exit, and never after
// kill-session or kill-server — which are exactly the ends that must leave "up" behind.
// Empty when this binary cannot be found, which costs only the exited/up distinction.
func exitHook(configDir, name string) string {
	bin, err := topicScript()
	if err != nil {
		return ""
	}
	return "; " + shellQuote(shellPath(bin)) + " session-ended " + shellQuote(name) + " " +
		shellQuote(configDir) + " " + shellQuote(topicsRootFor(configDir))
}

// topicsRootFor is the registry a session in configDir belongs to: the active one
// (which CLAUDE_TOPICS_ROOT may relocate) for the active profile, else its own. Launches
// into another profile run as a child process with that profile active (runTopicIn), so
// the second case is a fallback.
func topicsRootFor(configDir string) string {
	if p := activeProfile(); p.configDir == configDir {
		return p.topicsRoot
	}
	return filepath.Join(configDir, "topics")
}

// session-ended <name> <config-dir> <topics-root>
//
// Run by exitHook in the pane, after Claude has quit. Only an "up" topic becomes
// "exited": a deliberate down (`topic down` quits Claude the same way) is already
// recorded, and an adopted topic is not ours to change.
func cmdSessionEnded(args []string) error {
	need(args, 3, "session-ended <name> <config-dir> <topics-root>")
	name, configDir, topicsRoot := args[0], args[1], args[2]
	file := topicFile(topicsRoot, name)
	if !fileExists(file) || regGet(topicsRoot, name, "state") != "up" {
		return nil
	}
	// A relaunch kills the old pane — and any hook still in it — before starting the
	// new session, so a running Claude here would be a surprise. Leave it be if so.
	// paneShowsClaude, not isUp: while this hook runs, the pane's foreground program is
	// topic itself, which isUp counts as a live session.
	if paneShowsClaude(tmuxName(profileTag(configDir), name)) {
		return nil
	}
	return cmdSet([]string{file, "state", "exited", "lastExited", nowISO()})
}

// restoreCandidates lists the topics restore would bring up: up in the registry, or
// flagged by cycle down, and not running now. up is injected so tests need no tmux.
func restoreCandidates(topicsRoot string, only []string, up func(name string) bool) []string {
	want := map[string]bool{}
	for _, n := range only {
		want[n] = true
	}
	var out []string
	for _, name := range topicNames(topicsRoot) {
		if len(want) > 0 && !want[name] {
			continue
		}
		m := load(topicFile(topicsRoot, name))
		if scalar(m["sessionId"]) == "" {
			continue // nothing to resume
		}
		if scalar(m["state"]) != "up" && scalar(m[restoreKey]) == "" {
			continue
		}
		if !up(name) {
			out = append(out, name)
		}
	}
	return out
}

// restoreNudge is the one line that tells you restore has something to do. Shown by
// `topic list`, and logged by the Dispatcher when it comes back after a reboot.
func restoreNudge(configDir, topicsRoot string) string {
	tag := profileTag(configDir)
	n := len(restoreCandidates(topicsRoot, nil, func(name string) bool { return isUp(tag, name) }))
	if n == 0 {
		return ""
	}
	what := "topics were"
	if n == 1 {
		what = "topic was"
	}
	return fmt.Sprintf("%d %s up when the machine went down, or taken down by `topic cycle down`.\n"+
		"  topic restore brings them back (--dry-run lists them, --discard forgets them)", n, what)
}

// restore [names...] [--dry-run] [--discard] [--all-profiles] <config-dir> <topics-root>
func cmdRestore(args []string) error {
	dry, discard, allProfiles, detached := false, false, false, false
	var positional []string
	for _, a := range args {
		switch a {
		case "--dry-run":
			dry = true
		case "--discard":
			discard = true
		case "--all-profiles":
			allProfiles = true
		case detachedFlag:
			detached = true
		default:
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("unknown flag for restore: %s (try: topic restore --help)", a)
			}
			positional = append(positional, a)
		}
	}
	need(positional, 2, `restore ["<name>"...] [--dry-run] [--discard] [--all-profiles]`)
	configDir, topicsRoot := positional[len(positional)-2], positional[len(positional)-1]
	only := positional[:len(positional)-2]

	// From inside a topic — the Dispatcher, asked to restore — the work outlives any
	// sensible tool-call timeout: each launch waits for its session to start. Detach,
	// and leave a log to follow.
	if !dry && !discard && !detached && whoami(topicsRoot, configDir) != "" {
		return detachRestore(args, topicsRoot)
	}

	if allProfiles {
		if len(only) > 0 {
			return fmt.Errorf("restore: name topics or use --all-profiles, not both — topic names belong to one profile")
		}
		var flags []string
		if dry {
			flags = append(flags, "--dry-run")
		}
		if discard {
			flags = append(flags, "--discard")
		}
		for _, p := range listProfiles() {
			fmt.Printf("-- profile: %s\n", p)
			if err := inProfile(p, append([]string{"restore"}, flags...)...); err != nil {
				return err
			}
		}
		return nil
	}

	tag := profileTag(configDir)
	names := restoreCandidates(topicsRoot, only, func(name string) bool { return isUp(tag, name) })
	if len(names) == 0 {
		fmt.Println("  nothing to restore in this profile: every topic recorded as up is running.")
		return nil
	}

	if discard {
		for _, name := range names {
			if dry {
				fmt.Printf("  would forget: %s\n", name)
				continue
			}
			if err := cmdSet([]string{topicFile(topicsRoot, name), "state", "down", restoreKey, ""}); err != nil {
				return err
			}
			fmt.Printf("  marked down, not restored: %s\n", name)
		}
		return nil
	}

	if dry {
		for _, name := range names {
			fmt.Printf("  would pick up: %s\n", name)
		}
		return nil
	}

	fmt.Printf("Restoring %d topic(s)…\n", len(names))
	var parked, failed []string
	for _, name := range names {
		err := cmdUp([]string{name, configDir, topicsRoot})
		switch {
		case err == nil:
		case isLeftRunning(err):
			parked = append(parked, name)
			fmt.Fprintf(os.Stderr, "  %v\n", err)
		default:
			failed = append(failed, name)
			fmt.Fprintf(os.Stderr, "  %q did not come up: %v\n", name, err)
		}
		if err == nil || isLeftRunning(err) {
			if err := cmdSet([]string{topicFile(topicsRoot, name), restoreKey, ""}); err != nil {
				return err
			}
		}
	}
	fmt.Printf("\nRestored %d of %d.\n", len(names)-len(failed)-len(parked), len(names))
	if len(parked) > 0 {
		fmt.Printf("  Running, but waiting on a question: %s\n", strings.Join(quoteAll(parked), ", "))
	}
	if len(failed) > 0 {
		fmt.Printf("  Did not come up (still listed for restore): %s\n", strings.Join(quoteAll(failed), ", "))
		return fmt.Errorf("%d topic(s) not restored", len(failed))
	}
	return nil
}

// detachRestore re-runs this restore in its own process group, as handoff-swap does, so
// the caller's tool call returns at once. args are restore's own, profile included.
func detachRestore(args []string, topicsRoot string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	own := append([]string{"restore"}, args[:len(args)-2]...) // main appends the profile again
	cmd := exec.Command(self, append(own, detachedFlag)...)
	cmd.Env = append(os.Environ(), "CLAUDE_TOPICS_ROOT="+topicsRoot)
	log := filepath.Join(topicsRoot, "restore.log")
	if f, err := os.OpenFile(log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		fmt.Fprintf(f, "\n=== %s: %s\n", nowISO(), strings.Join(own, " "))
		cmd.Stdout, cmd.Stderr = f, f
	}
	if err := startDetached(cmd); err != nil {
		return err
	}
	fmt.Println("Restoring in the background — each topic takes a few seconds to start.")
	fmt.Printf("  Progress: %s   (topic restore --dry-run shows what is still to come)\n", log)
	return nil
}

func quoteAll(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = fmt.Sprintf("%q", n)
	}
	return out
}
