package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// cycle takes every profile's topics down cleanly and brings them back.
//
// It exists for the ORDER, which is easy to get wrong and not obvious when you do:
// the launchd agents run `ensure-dispatcher` every five minutes, so a Dispatcher put
// down without unloading its agent first comes back underneath you — typically in the
// middle of logging a profile in, which is exactly when it causes the most confusion.
//
// Two verbs rather than one, because the point of taking everything down is what you
// do in between: log in or out, rotate credentials, upgrade Claude Code.

// launchAgent is one installed Dispatcher agent, and therefore one installed profile.
// The agents ARE the registry of installed profiles — each names the profile it
// manages — so there is no second list to drift out of sync.
type launchAgent struct {
	plist     string
	configDir string
}

func launchAgents() []launchAgent {
	home, _ := os.UserHomeDir()
	paths, _ := filepath.Glob(filepath.Join(home, "Library", "LaunchAgents", "com.verveguy.claude-dispatcher*.plist"))
	var out []launchAgent
	for _, p := range paths {
		dir := plistConfigDir(p)
		if dir == "" {
			// A pre-profile agent, written before profiles existed: the default one.
			dir = filepath.Join(home, ".claude")
		}
		out = append(out, launchAgent{p, dir})
	}
	if len(out) == 0 {
		// No agents installed at all: still act on the profile in the environment.
		out = append(out, launchAgent{"", activeProfile().configDir})
	}
	return out
}

// plistConfigDir digs EnvironmentVariables.CLAUDE_CONFIG_DIR out of a plist. Written
// against the token stream rather than a struct because a plist dict is a flat
// key/value sequence, which does not map onto Go fields.
func plistConfigDir(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	dec := xml.NewDecoder(f)
	var keys []string // the key path we are currently inside
	pendingKey := ""
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "key":
				var s string
				if dec.DecodeElement(&s, &t) == nil {
					pendingKey = s
				}
			case "dict":
				keys = append(keys, pendingKey)
				pendingKey = ""
			case "string":
				var s string
				if dec.DecodeElement(&s, &t) != nil {
					continue
				}
				if pendingKey == "CLAUDE_CONFIG_DIR" &&
					len(keys) > 0 && keys[len(keys)-1] == "EnvironmentVariables" {
					return s
				}
				pendingKey = ""
			}
		case xml.EndElement:
			if t.Name.Local == "dict" && len(keys) > 0 {
				keys = keys[:len(keys)-1]
			}
		}
	}
}

// inProfile runs a topic command against one profile. CLAUDE_CONFIG_DIR is *unset* for
// the default profile: setting it to ~/.claude selects a different, un-onboarded
// config file.
func inProfile(configDir string, args ...string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, args...)
	home, _ := os.UserHomeDir()
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") && !strings.HasPrefix(e, "CLAUDE_TOPICS_ROOT=") {
			env = append(env, e)
		}
	}
	if configDir != filepath.Join(home, ".claude") {
		env = append(env, "CLAUDE_CONFIG_DIR="+configDir)
	}
	cmd.Env = env
	out, _ := cmd.CombinedOutput()
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line != "" {
			fmt.Println("  " + line)
		}
	}
	return nil
}

// cycle down|up|profiles [--dry-run]
func cmdCycle(args []string) error {
	dry := false
	var positional []string
	for _, a := range args {
		if a == "--dry-run" {
			dry = true
			continue
		}
		positional = append(positional, a)
	}
	verb := ""
	if len(positional) > 0 {
		verb = positional[0]
	}

	run := func(what string, argv ...string) {
		if dry {
			fmt.Printf("  would: %s %s\n", what, strings.Join(argv, " "))
			return
		}
		_ = exec.Command(what, argv...).Run()
	}

	switch verb {
	case "profiles":
		for _, a := range launchAgents() {
			label := "(no agent installed)"
			if a.plist != "" {
				label = strings.TrimSuffix(filepath.Base(a.plist), ".plist")
			}
			fmt.Printf("  %-40s %s\n", label, a.configDir)
		}
		return nil

	case "down":
		fmt.Println("== 1. Unloading launchd agents (or they restart Dispatchers underneath you)")
		for _, a := range launchAgents() {
			if a.plist == "" {
				fmt.Println("  (no agents installed)")
				continue
			}
			run("launchctl", "unload", a.plist)
		}
		fmt.Println()
		fmt.Println("== 2. Putting topics down (lossless — every session stays resumable)")
		for _, a := range launchAgents() {
			fmt.Printf("  -- profile: %s\n", a.configDir)
			argv := []string{"down-all"}
			if dry {
				argv = append(argv, "--dry-run")
			}
			if err := inProfile(a.configDir, argv...); err != nil {
				return err
			}
		}
		fmt.Println()
		fmt.Println("Next: log each profile in from a plain session in your home directory,")
		fmt.Println("then run:  topic cycle up")
		return nil

	case "up":
		fmt.Println("== 1. Reloading launchd agents")
		for _, a := range launchAgents() {
			if a.plist == "" {
				fmt.Println("  (no agents installed)")
				continue
			}
			run("launchctl", "load", "-w", a.plist)
		}
		fmt.Println()
		fmt.Println("== 2. Starting each profile's Dispatcher")
		for _, a := range launchAgents() {
			fmt.Printf("  -- profile: %s\n", a.configDir)
			if dry {
				fmt.Printf("    would: CLAUDE_CONFIG_DIR=%s topic ensure-dispatcher\n", a.configDir)
				continue
			}
			if err := inProfile(a.configDir, "ensure-dispatcher"); err != nil {
				return err
			}
		}
		fmt.Println()
		fmt.Println(`Other topics stay down until you pick them up:  topic up "<name>"`)
		fmt.Println("(they are unchanged — up resumes the same session)")
		return nil
	}
	return fmt.Errorf("usage: topic cycle down|up|profiles [--dry-run]")
}
