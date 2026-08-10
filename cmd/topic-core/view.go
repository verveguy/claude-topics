package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Read-only commands: listing topics, reporting one, and enumerating profiles. They
// touch only the registry, the filesystem and `tmux has-session`, so they were the
// next safest slice to migrate after the JSON layer.

// slugify mirrors the shell's: "Fantasy Economics" -> "fantasy-economics".
func slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			dash = false
		case !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// profileTag labels a non-default profile: ~/.claude-v3rv -> "v3rv". The default
// profile has no tag, which is what keeps its tmux names bare.
func profileTag(configDir string) string {
	home, _ := os.UserHomeDir()
	if configDir == filepath.Join(home, ".claude") {
		return ""
	}
	return strings.TrimPrefix(strings.TrimPrefix(filepath.Base(configDir), "."), "claude-")
}

// tmuxName mirrors the shell's: tmux forbids ':' and '.', and non-default profiles are
// namespaced so two profiles cannot fight over one topic name.
func tmuxName(tag, name string) string {
	prefix := ""
	if tag != "" {
		prefix = tag + "/"
	}
	return prefix + strings.NewReplacer(":", "-", ".", "-").Replace(name)
}

func isUp(tag, name string) bool {
	return exec.Command("tmux", "has-session", "-t", "="+tmuxName(tag, name)).Run() == nil
}

// topicNames returns the display names in a registry root, sorted for stable output.
func topicNames(topicsRoot string) []string {
	entries, err := os.ReadDir(topicsRoot)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if n := scalar(load(filepath.Join(topicsRoot, e.Name(), "topic.json"))["name"]); n != "" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// listProfiles finds every Claude Code profile: the default, plus ~/.claude-* that
// actually look like config dirs — a stray ~/.claude-backup should not count.
func listProfiles() []string {
	home, _ := os.UserHomeDir()
	profiles := []string{filepath.Join(home, ".claude")}
	matches, _ := filepath.Glob(filepath.Join(home, ".claude-*"))
	sort.Strings(matches)
	for _, m := range matches {
		if fi, err := os.Stat(m); err != nil || !fi.IsDir() {
			continue
		}
		if dirExists(filepath.Join(m, "projects")) || fileExists(filepath.Join(m, ".claude.json")) {
			profiles = append(profiles, m)
		}
	}
	return profiles
}

func dirExists(p string) bool  { fi, err := os.Stat(p); return err == nil && fi.IsDir() }
func fileExists(p string) bool { fi, err := os.Stat(p); return err == nil && !fi.IsDir() }

// configFileOf encodes a rule worth not re-deriving: with CLAUDE_CONFIG_DIR unset,
// Claude Code reads ~/.claude.json; set to ~/.claude it reads ~/.claude/.claude.json —
// a different file, with different onboarding and account state.
func configFileOf(configDir string) string {
	home, _ := os.UserHomeDir()
	if configDir == filepath.Join(home, ".claude") {
		return filepath.Join(home, ".claude.json")
	}
	return filepath.Join(configDir, ".claude.json")
}

func onboarded(configDir string) bool {
	return load(configFileOf(configDir))["hasCompletedOnboarding"] == true
}

// cmdList prints this profile's topics, or every profile's with --all.
//
//	list [--all] <config-dir> <topics-root>
func cmdList(args []string) error {
	all := false
	if len(args) > 0 && args[0] == "--all" {
		all, args = true, args[1:]
	}
	need(args, 2, "list [--all] <config-dir> <topics-root>")
	configDir, topicsRoot := args[0], args[1]

	if all {
		any := false
		for _, p := range listProfiles() {
			tag := profileTag(p)
			for _, name := range topicNames(filepath.Join(p, "topics")) {
				any = true
				state := "down  "
				if isUp(tag, name) {
					state = "UP    "
				}
				label := tag
				if label == "" {
					label = "(default)"
				}
				fmt.Printf("  %-7s %-12s %s\n", state, label, name)
			}
		}
		if !any {
			fmt.Println("  (no topics in any profile)")
		}
		return nil
	}

	tag := profileTag(configDir)
	// Say which profile these belong to when it is not the default, or a short list
	// looks like topics have gone missing.
	if tag != "" {
		fmt.Printf("  (profile: %s)\n", configDir)
	}
	names := topicNames(topicsRoot)
	if len(names) == 0 {
		fmt.Println(`  (no topics yet — ` + "`topic up \"<name>\" [dir]`" + ` creates one)`)
		return nil
	}
	for _, name := range names {
		state := "down  "
		switch {
		case isUp(tag, name):
			state = "UP    "
		case scalar(load(filepath.Join(topicsRoot, slugify(name), "topic.json"))["state"]) == "adopted":
			// An adopted topic runs outside tmux, so isUp cannot see it. Report that
			// honestly rather than calling a live session "down".
			state = "ADOPTED"
		}
		fmt.Printf("  %-7s %s\n", state, name)
	}
	return nil
}

// status <name> <config-dir> <topics-root>
func cmdStatusCmd(args []string) error {
	need(args, 3, "status <name> <config-dir> <topics-root>")
	name, configDir, topicsRoot := args[0], args[1], args[2]
	file := filepath.Join(topicsRoot, slugify(name), "topic.json")
	if !fileExists(file) {
		return fmt.Errorf("no such topic: %q", name)
	}
	state := "down"
	if isUp(profileTag(configDir), name) {
		state = "UP"
	}
	fmt.Printf("%s  [%s]\n", name, state)
	return cmdStatus([]string{file})
}

// profiles <active-config-dir>
func cmdProfiles(args []string) error {
	need(args, 1, "profiles <active-config-dir>")
	active := args[0]
	for _, p := range listProfiles() {
		tag := profileTag(p)
		label := tag
		if label == "" {
			label = "(default)"
		}
		marker := " "
		if p == active {
			marker = "*"
		}
		note := ""
		if !onboarded(p) {
			note = "  ** first-run setup not finished — sessions will not start"
		}
		fmt.Printf("  %s %-26s %-11s %2d topic(s)%s\n",
			marker, p, label, len(topicNames(filepath.Join(p, "topics"))), note)
	}
	fmt.Println()
	fmt.Println("  * = active. Use another with: CLAUDE_CONFIG_DIR=<dir> topic <cmd>")
	return nil
}
