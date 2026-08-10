package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

// fork and handoff-swap: the two commands that create a session from a document.
//
// They differ in what the document is for. A handoff doc looks BACK over a topic's own
// history and is read by its successor; a fork brief looks FORWARD and is read by a
// session that has never existed, about a thread it has never seen.

// newestDoc returns the most recent .md in a topic's subdirectory, or "".
func newestDoc(topicsRoot, name, kind string) string {
	matches, _ := filepath.Glob(filepath.Join(topicDir(topicsRoot, name), kind, "*.md"))
	if len(matches) == 0 {
		return ""
	}
	sort.Slice(matches, func(i, j int) bool {
		fi, err1 := os.Stat(matches[i])
		fj, err2 := os.Stat(matches[j])
		if err1 != nil || err2 != nil {
			return matches[i] > matches[j]
		}
		return fi.ModTime().After(fj.ModTime())
	})
	return matches[0]
}

func nonEmptyFile(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("no such file: %s", path)
	}
	if fi.Size() == 0 {
		return fmt.Errorf("%s is empty", path)
	}
	return nil
}

//	fork <new> [dir] <config-dir> <topics-root> [--from <parent>] [--brief <file>]
//
// The brief must already be written: only the session holding the context can write
// it, exactly as with handoff.
func cmdFork(args []string) error {
	var name, dir, parent, brief string
	var positional []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--from":
			if i+1 < len(args) {
				i++
				parent = args[i]
			}
		case "--brief":
			if i+1 < len(args) {
				i++
				brief = args[i]
			}
		default:
			positional = append(positional, args[i])
		}
	}
	if len(positional) < 3 {
		return fmt.Errorf(`usage: topic fork "<new topic>" [dir] [--from "<parent>"] [--brief <file>]`)
	}
	name = positional[0]
	configDir, topicsRoot := positional[len(positional)-2], positional[len(positional)-1]
	if len(positional) == 4 {
		dir = positional[1]
	}

	if regGet(topicsRoot, name, "sessionId") != "" {
		return fmt.Errorf("fork: %q already exists with a session — pick another name, or `topic up %q`", name, name)
	}

	// Default the parent to whoever is calling, when that is knowable.
	if parent == "" {
		parent = whoami(topicsRoot, configDir)
	}
	if brief == "" {
		brief = newestDoc(topicsRoot, name, "briefs")
	}
	if brief == "" {
		return fmt.Errorf("fork: no brief for %q — write one first: topic fork-path %q", name, name)
	}
	if err := nonEmptyFile(brief); err != nil {
		return fmt.Errorf("fork: brief %v — refusing to fork", err)
	}
	if abs, err := filepath.Abs(brief); err == nil {
		brief = abs
	}

	// A fork inherits the parent's working directory unless told otherwise. Without
	// this it lands in $PWD — which, forking remotely through the Dispatcher, is the
	// Dispatcher's home rather than the work.
	if dir == "" && parent != "" {
		dir = regGet(topicsRoot, parent, "dir")
	}
	if dir == "" {
		dir, _ = os.Getwd()
	}

	release, err := lockAcquire(topicsRoot, name)
	if err != nil {
		return err
	}
	defer release()

	sid := newUUID()
	seed := fmt.Sprintf("You are starting the topic %q.", name)
	if parent != "" {
		seed += fmt.Sprintf(" It was forked from the topic %q to carry one thread on its own.", parent)
	}
	seed += fmt.Sprintf(" Read the brief at %s — it was written for you by the session you were "+
		"forked from, and is the only context you carry. Summarise briefly what you picked up, "+
		"then continue from there.", brief)

	if parent != "" {
		fmt.Printf("Forking %q -> %q (new session %s…) in %s\n", parent, name, truncate(sid, 8), dir)
	} else {
		fmt.Printf("Forking %q (new session %s…) in %s\n", name, truncate(sid, 8), dir)
	}
	fmt.Printf("  brief: %s\n", brief)

	if err := launchSession(configDir, name, dir,
		"--remote-control", name, "--name", name, "--session-id", sid, seed); err != nil {
		return err
	}

	fields := []string{topicFile(topicsRoot, name),
		"name", name, "dir", dir, "sessionId", sid, "state", "up",
		"generation", "1", "seededFrom", brief, "lastPickedUp", nowISO()}
	if parent != "" {
		fields = append(fields, "forkedFrom", parent)
	}
	if err := cmdSet(fields); err != nil {
		return err
	}
	if parent != "" {
		if err := cmdPushFork([]string{topicFile(topicsRoot, parent), name}); err != nil {
			return err
		}
	}

	fmt.Printf("%q is up. Remote Control name: %q\n", name, name)
	if parent != "" {
		fmt.Println()
		fmt.Printf("  %q still carries this thread in its own context. To shed it:\n", parent)
		fmt.Printf("    topic handoff-swap %q   # after writing its handoff doc\n", parent)
	}
	return nil
}

//	handoff-swap <name> [doc] <config-dir> <topics-root> [--focus <text>]
//
// Retires the current session — archiving its UUID so it stays resumable in an
// emergency — and starts a fresh one under the same topic name, seeded with the doc.
func cmdHandoffSwap(args []string) error {
	var focus string
	var positional []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--focus" {
			if i+1 < len(args) {
				i++
				focus = args[i]
			}
			continue
		}
		positional = append(positional, args[i])
	}
	if len(positional) < 3 {
		return fmt.Errorf(`usage: topic handoff-swap "<name>" [handoff-doc] [--focus "<what to pick up>"]`)
	}
	name := positional[0]
	configDir, topicsRoot := positional[len(positional)-2], positional[len(positional)-1]
	doc := ""
	if len(positional) == 4 {
		doc = positional[1]
	}

	if doc == "" {
		doc = newestDoc(topicsRoot, name, "handoffs")
	}
	if doc == "" {
		return fmt.Errorf("no handoff doc found for %q — write one first (topic handoff-path %q)", name, name)
	}
	if err := nonEmptyFile(doc); err != nil {
		return fmt.Errorf("handoff doc %v — refusing to swap", err)
	}

	file := topicFile(topicsRoot, name)
	oldSID := regGet(topicsRoot, name, "sessionId")
	dir := regGet(topicsRoot, name, "dir")
	if dir == "" {
		dir, _ = os.Getwd()
	}
	gen := 1
	if g, err := strconv.Atoi(regGet(topicsRoot, name, "generation")); err == nil && g > 0 {
		gen = g
	}

	fmt.Printf("Handing off %q (generation %d -> %d)\n", name, gen, gen+1)
	fmt.Printf("  handoff doc: %s\n", doc)

	// Retire the old session first, so the topic stays a singleton.
	stopSession(configDir, name)
	if oldSID != "" {
		if err := cmdPushHistory([]string{file, oldSID, doc}); err != nil {
			return err
		}
	}

	newSID := newUUID()
	seed := fmt.Sprintf("You are resuming the topic %q in a fresh session. Read the handoff document "+
		"at %s — it was written by your previous session on this topic and contains the salient "+
		"context. Summarise briefly what you picked up, then continue from there.", name, doc)
	if focus != "" {
		seed += fmt.Sprintf(" The handoff is focused on: %s. Start there. The doc also lists other "+
			"open threads that were deliberately set aside — do not act on those unless asked, but "+
			"do not treat them as finished either.", focus)
	}

	if err := launchSession(configDir, name, dir,
		"--remote-control", name, "--name", name, "--session-id", newSID, seed); err != nil {
		return err
	}
	if err := cmdSet([]string{file, "name", name, "dir", dir, "sessionId", newSID,
		"state", "up", "generation", strconv.Itoa(gen + 1),
		"lastPickedUp", nowISO(), "lastHandoff", doc}); err != nil {
		return err
	}
	fmt.Printf("%q is up on a fresh session (%s…). Old session archived in history.\n",
		name, truncate(newSID, 8))
	return nil
}
