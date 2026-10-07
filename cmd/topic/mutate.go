package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// State-mutating commands: forget, prune, rebridge, move, rename.
//
// move and rename put a topic down and bring it back up, which is orchestration and
// still lives in bash. They reach it by exec'ing the front-end script; when `up` and
// `down` migrate, runTopicIn becomes an ordinary function call.

// topicScript locates the bash front-end, which sits beside this binary.
func topicScript() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	p := filepath.Join(filepath.Dir(self), "topic")
	if _, err := os.Stat(p); err != nil {
		if p, err2 := exec.LookPath("topic"); err2 == nil {
			return p, nil
		}
		return "", fmt.Errorf("cannot find the topic front-end next to %s", self)
	}
	return p, nil
}

// runTopicIn invokes a command against a specific profile, streaming its output with
// each line indented.
//
// CLAUDE_CONFIG_DIR is *unset* for the default profile: setting it to ~/.claude
// selects a different, un-onboarded config file. topicsRoot is passed through when
// acting WITHIN a profile and left empty when acting on another one, whose registry
// is its own <profile>/topics — inheriting the caller's root there would point the
// command at the wrong registry entirely.
func runTopicIn(configDir, topicsRoot, indent string, args ...string) error {
	script, err := topicScript()
	if err != nil {
		return err
	}
	cmd := exec.Command(script, args...)
	home, _ := os.UserHomeDir()
	env := os.Environ()
	var kept []string
	for _, e := range env {
		if !strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") && !strings.HasPrefix(e, "CLAUDE_TOPICS_ROOT=") {
			kept = append(kept, e)
		}
	}
	if configDir != filepath.Join(home, ".claude") {
		kept = append(kept, "CLAUDE_CONFIG_DIR="+configDir)
	}
	if topicsRoot != "" {
		kept = append(kept, "CLAUDE_TOPICS_ROOT="+topicsRoot)
	}
	cmd.Env = kept
	out, err := cmd.CombinedOutput()
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line != "" {
			fmt.Println(indent + line)
		}
	}
	return err
}

// ---------------------------------------------------------------- shared helpers

func topicDir(topicsRoot, name string) string {
	return filepath.Join(topicsRoot, slugify(name))
}

func topicFile(topicsRoot, name string) string {
	return filepath.Join(topicDir(topicsRoot, name), "topic.json")
}

func regGet(topicsRoot, name, key string) string {
	return scalar(load(topicFile(topicsRoot, name))[key])
}

// transcriptOf finds a session's transcript within one profile. `claude --resume` only
// looks inside its own profile, which is why moving one matters.
func transcriptOf(configDir, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(configDir, "projects", "*", sessionID+".jsonl"))
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}

// settle waits for a file to stop changing. An exiting session keeps writing after
// `down` returns, and a late flush recreates a transcript at its old path.
func settle(path string) {
	last, stable := "", 0
	for range 25 {
		fi, err := os.Stat(path)
		cur := "gone"
		if err == nil {
			cur = fmt.Sprintf("%d-%d", fi.Size(), fi.ModTime().UnixNano())
		}
		if cur == last {
			if stable++; stable >= 2 {
				return
			}
		} else {
			stable = 0
		}
		last = cur
		time.Sleep(200 * time.Millisecond)
	}
}

// dropBridge removes the bridge-session records, keeping a copy first. Returns the
// bridge it dropped, or "".
func dropBridge(transcript, backupDir string) (string, error) {
	if !fileExists(transcript) {
		return "", nil
	}
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", err
	}
	b, err := os.ReadFile(transcript)
	if err != nil {
		return "", err
	}
	base := strings.TrimSuffix(filepath.Base(transcript), ".jsonl")
	backup := filepath.Join(backupDir, fmt.Sprintf("%s-%s.jsonl", base, time.Now().Format("20060102-150405")))
	if err := os.WriteFile(backup, b, 0o600); err != nil {
		return "", err
	}
	var out []byte
	old := ""
	for line := range strings.SplitSeq(string(b), "\n") {
		if strings.Contains(line, `"bridge-session"`) {
			var rec map[string]any
			if jsonUnmarshal(line, &rec) && scalar(rec["type"]) == "bridge-session" {
				if id := scalar(rec["bridgeSessionId"]); id != "" {
					old = id
				}
				continue
			}
		}
		if line != "" {
			out = append(out, line...)
			out = append(out, '\n')
		}
	}
	if old == "" {
		os.Remove(backup) // nothing to do; do not leave a pointless copy
		return "", nil
	}
	tmp := transcript + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return "", err
	}
	return old, os.Rename(tmp, transcript)
}

// countDocs counts the handoff docs and briefs a topic carries.
func countDocs(dir string) int {
	n := 0
	for _, sub := range []string{"handoffs", "briefs"} {
		matches, _ := filepath.Glob(filepath.Join(dir, sub, "*.md"))
		n += len(matches)
	}
	return n
}

// whoami names the topic the caller is sitting in, via the tmux session name.
func whoami(topicsRoot, configDir string) string {
	if os.Getenv("TMUX") == "" {
		return ""
	}
	out, err := exec.Command("tmux", "display-message", "-p", "#S").Output()
	if err != nil {
		return ""
	}
	session := strings.TrimSpace(string(out))
	tag := profileTag(configDir)
	for _, name := range topicNames(topicsRoot) {
		if tmuxName(tag, name) == session {
			return name
		}
	}
	return ""
}

// ---------------------------------------------------------------- forget

// forget <name> <config-dir> <topics-root> [--purge] [--dead]
func cmdForget(args []string) error {
	var name, configDir, topicsRoot string
	purge, dead := false, false
	var positional []string
	for _, a := range args {
		switch a {
		case "--purge":
			purge = true
		case "--dead":
			dead = true
		default:
			positional = append(positional, a)
		}
	}
	need(positional, 3, "forget <name> <config-dir> <topics-root> [--purge] [--dead]")
	name, configDir, topicsRoot = positional[0], positional[1], positional[2]

	dir := topicDir(topicsRoot, name)
	if !fileExists(filepath.Join(dir, "topic.json")) {
		return fmt.Errorf("no such topic: %q", name)
	}
	if isUp(profileTag(configDir), name) {
		return fmt.Errorf("%q is still up — `topic down %q` first", name, name)
	}
	// Forgetting is the last chance to clean up: after the registry entry is gone
	// nothing can name this topic, so a husk left here would linger unreferenced.
	if clearHusk(profileTag(configDir), name) {
		fmt.Printf("note: %q had already exited; cleared its leftover tmux session.\n", name)
	}
	if regGet(topicsRoot, name, "state") == "adopted" {
		fmt.Fprintf(os.Stderr, "note: %q was adopted; its original session may still be running.\n", name)
	}

	sid := regGet(topicsRoot, name, "sessionId")
	n := countDocs(dir)

	if purge {
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
		fmt.Printf("Forgot %q and purged %d document(s).\n", name, n)
	} else {
		if err := os.Remove(filepath.Join(dir, "topic.json")); err != nil {
			return err
		}
		os.Remove(dir) // succeeds only if nothing else remains
		fmt.Printf("Forgot %q.\n", name)
		if n > 0 {
			fmt.Printf("  Kept %d document(s) in %s/ (--purge to remove).\n", n, dir)
		}
	}
	// The session itself is untouched and still resumable — unless prune already
	// established that its transcript is gone.
	if sid != "" && !dead {
		fmt.Printf("  Session %s is untouched: claude --resume %s\n", sid, sid)
	}
	return nil
}

// ---------------------------------------------------------------- prune

// prune <config-dir> <topics-root> [--yes] [--purge]
func cmdPrune(args []string) error {
	yes, purge := false, false
	var positional []string
	for _, a := range args {
		switch a {
		case "--yes", "-y":
			yes = true
		case "--purge":
			purge = true
		default:
			positional = append(positional, a)
		}
	}
	need(positional, 2, "prune <config-dir> <topics-root> [--yes] [--purge]")
	configDir, topicsRoot := positional[0], positional[1]

	tag := profileTag(configDir)
	var dead []string
	for _, name := range topicNames(topicsRoot) {
		if isUp(tag, name) {
			continue
		}
		sid := regGet(topicsRoot, name, "sessionId")
		// No sessionId at all also counts as dead: nothing to resume.
		if sid == "" || transcriptOf(configDir, sid) == "" {
			dead = append(dead, name)
		}
	}
	if len(dead) == 0 {
		fmt.Println("Nothing to prune — every down topic still has a resumable transcript.")
		return nil
	}

	fmt.Println("Dead topics (down, transcript gone — not resumable):")
	for _, name := range dead {
		sid := regGet(topicsRoot, name, "sessionId")
		if sid == "" {
			sid = "<no session>"
		}
		fmt.Printf("  %-30s %s\n", name, sid)
	}
	if !yes {
		extra := ""
		if purge {
			extra = " --purge"
		}
		fmt.Println()
		fmt.Printf("Dry run. To remove: topic prune --yes%s\n", extra)
		fmt.Println("  (handoff docs are kept unless --purge)")
		return nil
	}
	fmt.Println()
	for _, name := range dead {
		a := []string{name, configDir, topicsRoot, "--dead"}
		if purge {
			a = append(a, "--purge")
		}
		if err := cmdForget(a); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------- rebridge

// rebridge <name>|--all <config-dir> <topics-root> [--down-only] [--dry-run]
func cmdRebridge(args []string) error {
	all, downOnly, dry := false, false, false
	var positional []string
	for _, a := range args {
		switch a {
		case "--all":
			all = true
		case "--down-only":
			downOnly = true
		case "--dry-run":
			dry = true
		default:
			positional = append(positional, a)
		}
	}
	need(positional, 2, "rebridge <name>|--all <config-dir> <topics-root>")
	var name string
	if !all {
		if len(positional) < 3 {
			return fmt.Errorf("rebridge: a topic name is required (or --all)")
		}
		name, positional = positional[0], positional[1:]
	}
	configDir, topicsRoot := positional[0], positional[1]
	tag := profileTag(configDir)
	self := whoami(topicsRoot, configDir)

	var targets []string
	if all {
		for _, n := range topicNames(topicsRoot) {
			if self != "" && n == self {
				fmt.Printf("  skipping %q — it is the session running this command\n", n)
				continue
			}
			// A live session would have to be restarted to re-register; a down one
			// re-registers for free on its next pickup.
			if downOnly && isUp(tag, n) {
				continue
			}
			targets = append(targets, n)
		}
	} else {
		if self != "" && name == self {
			return fmt.Errorf("rebridge: %q is the session running this command — run it from another session", name)
		}
		targets = []string{name}
	}

	for _, t := range targets {
		// A dry run only reports drift; it must not rewrite sessionId or history.
		if err := syncSessionID(configDir, topicsRoot, t, dry); err != nil {
			return err
		}
		sid := regGet(topicsRoot, t, "sessionId")
		if sid == "" {
			fmt.Printf("  %s: no session — skipping\n", t)
			continue
		}
		tr := transcriptOf(configDir, sid)
		if tr == "" {
			fmt.Printf("  %s: no transcript in this profile — skipping\n", t)
			continue
		}
		_, old := lastBridgeOf(tr)
		if old == "" {
			fmt.Printf("  %s: no bridge session recorded — nothing to do\n", t)
			continue
		}
		wasUp := isUp(tag, t)
		if dry {
			suffix := ""
			if wasUp {
				suffix = " (restart needed)"
			}
			fmt.Printf("  %s: would drop bridge %s…%s\n", t, truncate(old, 16), suffix)
			continue
		}
		// Archive the old cloud session before its record is dropped (see
		// retireRemoteControl); a down topic can only report where it is.
		retireRemoteControl(configDir, t, tr, wasUp)
		fmt.Printf("  %s: bridge %s… -> ", t, truncate(old, 16))
		if wasUp {
			_ = runTopicIn(configDir, topicsRoot, "", "down", t)
			settle(tr)
		}
		if _, err := dropBridge(tr, filepath.Join(topicDir(topicsRoot, t), "backups")); err != nil {
			return err
		}
		if wasUp {
			_ = runTopicIn(configDir, topicsRoot, "", "up", t)
			_, now := lastBridgeOf(tr)
			fmt.Printf("%s… (restarted)\n", truncate(now, 16))
		} else {
			fmt.Println("cleared (next `topic up` mints a fresh one)")
		}
	}
	return nil
}

// lastBridgeOf returns the transcript's last bridge id. Reported separately from
// stripping so callers can show what changed.
func lastBridgeOf(path string) (bool, string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, ""
	}
	const key = `"bridgeSessionId":"`
	last, s := "", string(b)
	for i := 0; ; {
		j := strings.Index(s[i:], key)
		if j < 0 {
			break
		}
		start := i + j + len(key)
		end := strings.Index(s[start:], `"`)
		if end < 0 {
			break
		}
		last = s[start : start+end]
		i = start + end
	}
	return last != "", last
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func jsonUnmarshal(line string, v any) bool {
	return json.Unmarshal([]byte(line), v) == nil
}
