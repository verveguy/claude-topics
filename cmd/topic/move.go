package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// move and rename. Both are "put down, change something structural, bring back up",
// and both must leave a topic able to present itself correctly afterwards — which
// means re-minting the Remote Control bridge, since that identity is pinned inside
// the transcript and ignores --remote-control on resume.

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if fi.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(p, target)
	})
}

// moveDir relocates a topic's directory, merging into whatever is already at the
// destination. A plain rename fails when a non-empty directory is in the way, and one
// routinely is: asking `handoff-path` where to write a document creates <slug>/handoffs
// under the new name before the topic exists.
func moveDir(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		from, to := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		if err := os.Rename(from, to); err != nil {
			// Occupied, or across filesystems: fall back to copying.
			if e.IsDir() {
				if err := copyTree(from, to); err != nil {
					return err
				}
			} else if err := copyFile(from, to); err != nil {
				return err
			}
			os.RemoveAll(from)
		}
	}
	return os.RemoveAll(src)
}

// resolveProfile turns "work" into ~/.claude-work, "default" into ~/.claude, and
// leaves an absolute path alone.
func resolveProfile(s string) string {
	home, _ := os.UserHomeDir()
	switch {
	case s == "" || s == "default":
		return filepath.Join(home, ".claude")
	case filepath.IsAbs(s) || strings.HasPrefix(s, "/"):
		// IsAbs covers C:\ on Windows; the "/" check keeps rooted paths working there too.
		return strings.TrimRight(s, `/\`)
	default:
		return filepath.Join(home, ".claude-"+s)
	}
}

// move <name> <config-dir> <topics-root> --to <profile> [--copy] [--dry-run]
//
//	[--stay-down] [--keep-bridge] [--force] [--carry-approvals]
func cmdMove(args []string) error {
	var name, to string
	copyMode, dry, stayDown, keepBridge, force, carry := false, false, false, false, false, false
	var positional []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--to":
			if i+1 < len(args) {
				i++
				to = args[i]
			}
		case "--copy":
			copyMode = true
		case "--dry-run":
			dry = true
		case "--stay-down":
			stayDown = true
		case "--keep-bridge":
			keepBridge = true
		case "--force":
			force = true
		case "--carry-approvals":
			carry = true
		default:
			positional = append(positional, args[i])
		}
	}
	if len(positional) < 3 {
		return fmt.Errorf(`usage: topic move "<name>" --to <profile> [--copy] [--stay-down] [--keep-bridge] [--dry-run]`)
	}
	name = positional[0]
	configDir, topicsRoot := positional[len(positional)-2], positional[len(positional)-1]
	if to == "" {
		return fmt.Errorf(`move: --to <profile> is required (a tag like "work", "default", or a path)`)
	}

	dst := resolveProfile(to)
	if dst == configDir {
		return fmt.Errorf("move: %q is already in %s", name, dst)
	}
	if !dirExists(dst) {
		return fmt.Errorf("move: no such profile directory: %s", dst)
	}
	if !dirExists(filepath.Join(dst, "projects")) && !fileExists(filepath.Join(dst, ".claude.json")) {
		return fmt.Errorf("move: %s does not look like a Claude Code config dir (no projects/ or .claude.json)", dst)
	}
	// Moving into a profile that cannot start a session strands the topic there.
	if !onboarded(dst) {
		fmt.Fprintf(os.Stderr, "move: profile %s has not completed first-run setup, so %q could not\n", dst, name)
		fmt.Fprintln(os.Stderr, "  be started there. Run it once interactively first:")
		fmt.Fprintf(os.Stderr, "    CLAUDE_CONFIG_DIR=%s claude\n", dst)
		return fmt.Errorf("destination profile not set up")
	}

	srcDir := topicDir(topicsRoot, name)
	if !fileExists(filepath.Join(srcDir, "topic.json")) {
		return fmt.Errorf("no such topic in this profile: %q", name)
	}
	dstDir := filepath.Join(dst, "topics", slugify(name))
	// A directory is not a topic: handoff-path and fork-path create <slug>/handoffs
	// and <slug>/briefs merely to answer "where does the document go". Only a
	// topic.json means the name is taken.
	if fileExists(filepath.Join(dstDir, "topic.json")) {
		return fmt.Errorf("move: %q already exists in %s (%s)", name, dst, dstDir)
	}

	// An ADOPTED topic runs outside tmux, so isUp cannot see it. Moving its transcript
	// while the process still holds it open would corrupt it — and unlike a managed
	// session, topic cannot stop it for the user.
	if regGet(topicsRoot, name, "state") == "adopted" {
		apid, asid := regGet(topicsRoot, name, "adoptedPid"), regGet(topicsRoot, name, "sessionId")
		if apid != "" && liveSessionRecord(configDir, apid, asid) {
			fmt.Fprintf(os.Stderr, "%q was adopted and is still running as pid %s, outside tmux.\n", name, apid)
			fmt.Fprintln(os.Stderr, "  Its transcript is open and being written; moving it would corrupt it,")
			fmt.Fprintln(os.Stderr, "  and topic cannot stop a session it did not launch.")
			fmt.Fprintln(os.Stderr, "  Exit that session (/exit in its window), then re-run this move.")
			return fmt.Errorf("refusing to move a live adopted session")
		}
	}

	// Per-project approvals are recorded per profile and do NOT move with the topic.
	// The source having approved external imports is exactly the signal that the
	// destination will be asked, since the flag only becomes true by being answered.
	workDir := regGet(topicsRoot, name, "dir")
	if workDir != "" {
		srcOK := scalar(projectEntry(load(configFileOf(configDir)), workDir)["hasClaudeMdExternalIncludesApproved"])
		dstOK := scalar(projectEntry(load(configFileOf(dst)), workDir)["hasClaudeMdExternalIncludesApproved"])
		if srcOK == "True" && dstOK != "True" {
			fmt.Fprintf(os.Stderr, "move: %s has not approved external CLAUDE.md imports for %s.\n", dst, workDir)
			fmt.Fprintln(os.Stderr, "  That approval is per profile and does not travel with the topic, so")
			fmt.Fprintf(os.Stderr, "  %q would arrive unable to start. Answer it once:\n", name)
			fmt.Fprintf(os.Stderr, "    (cd %s && CLAUDE_CONFIG_DIR=%s claude)\n", workDir, dst)
			fmt.Fprintln(os.Stderr, "  then re-run this move. (--force moves anyway and leaves it down.)")
			if carry {
				fmt.Fprintln(os.Stderr, "  --carry-approvals given: copying your existing approval across.")
			} else {
				fmt.Fprintln(os.Stderr, "  Or copy your existing approval across: --carry-approvals")
				if !force {
					return fmt.Errorf("destination profile would not be able to start it")
				}
				fmt.Fprintln(os.Stderr, "  --force given: moving anyway.")
			}
		}
	}

	tag := profileTag(configDir)
	wasUp := isUp(tag, name)
	if wasUp {
		// Refusing to move ourselves is not fussiness: `down` terminates the session
		// running this command, so the move would never happen.
		if self := whoami(topicsRoot, configDir); self != "" && self == name {
			fmt.Fprintf(os.Stderr, "%q is the topic you are running this from.\n", name)
			fmt.Fprintln(os.Stderr, "  Putting it down would kill this command mid-move.")
			fmt.Fprintln(os.Stderr, "  Run it from another session — the Dispatcher is there for exactly this:")
			fmt.Fprintf(os.Stderr, "    topic move %q --to %s\n", name, to)
			return fmt.Errorf("refusing to move the calling topic")
		}
		// A copy that is also running would diverge from its original the moment
		// either side is resumed.
		if copyMode {
			return fmt.Errorf("move: %q is up — --copy needs it down first (`topic down %q`)", name, name)
		}
	}

	verb := "Moving"
	if copyMode {
		verb = "Copying"
	}
	fmt.Printf("%s %q: %s -> %s\n", verb, name, configDir, dst)
	if wasUp {
		fmt.Printf("  it is UP: will put it down, move, and bring it back up in %s\n", dst)
	}
	if !keepBridge {
		fmt.Println("  remote control: will re-register under the destination profile's account")
	}
	fmt.Printf("  registry: %s -> %s\n", srcDir, dstDir)

	// Work out every destination before touching anything, so a dry run shows the
	// whole picture and a real run does not stop half-done.
	type mv struct{ from, to string }
	var moves []mv
	for _, sid := range sessionIDsOf(filepath.Join(srcDir, "topic.json")) {
		tr := transcriptOf(configDir, sid)
		if tr == "" {
			fmt.Printf("  transcript %s…: not found in this profile (already gone) — skipping\n", truncate(sid, 8))
			continue
		}
		rel := filepath.Base(filepath.Dir(tr))
		moves = append(moves, mv{tr, filepath.Join(dst, "projects", rel, sid+".jsonl")})
		fmt.Printf("  transcript %s…: -> %s/\n", truncate(sid, 8), filepath.Join(dst, "projects", rel))
	}

	if dry {
		fmt.Println()
		fmt.Println("Dry run — nothing moved.")
		return nil
	}

	if wasUp {
		fmt.Println()
		if err := cmdDown([]string{name, configDir, topicsRoot}); err != nil {
			return err
		}
		// `down` returns once claude has left the pane, but the process can still
		// flush a final record on its way out — which lands at the OLD path and
		// recreates a stub there after the move.
		for _, m := range moves {
			settle(m.from)
		}
		fmt.Println()
	}

	for _, m := range moves {
		if err := os.MkdirAll(filepath.Dir(m.to), 0o755); err != nil {
			return err
		}
		if _, err := os.Stat(m.to); err == nil {
			return fmt.Errorf("move: %s already exists — refusing to overwrite", m.to)
		}
		if copyMode {
			if err := copyFile(m.from, m.to); err != nil {
				return err
			}
		} else if err := os.Rename(m.from, m.to); err != nil {
			if err := copyFile(m.from, m.to); err != nil { // across filesystems
				return err
			}
			os.Remove(m.from)
		}
	}

	// Belt and braces: if a late flush still recreated a stub at the source path, drop
	// it — but only when it is strictly smaller than what we moved, so a real
	// transcript is never deleted on the strength of a race we merely suspect.
	if !copyMode {
		for _, m := range moves {
			fromInfo, err1 := os.Stat(m.from)
			toInfo, err2 := os.Stat(m.to)
			if err1 != nil || err2 != nil {
				continue
			}
			if fromInfo.Size() < toInfo.Size() {
				os.Remove(m.from)
				fmt.Printf("  (removed a %s… stub the exiting session left behind)\n",
					truncate(strings.TrimSuffix(filepath.Base(m.from), ".jsonl"), 8))
			} else {
				fmt.Fprintf(os.Stderr, "  WARNING: %s reappeared and is not smaller than the moved copy — left in place.\n", m.from)
			}
		}
	}

	if err := os.MkdirAll(filepath.Dir(dstDir), 0o755); err != nil {
		return err
	}
	if copyMode {
		if err := copyTree(srcDir, dstDir); err != nil {
			return err
		}
	} else if err := moveDir(srcDir, dstDir); err != nil {
		return err
	}

	// Record where it came from: after a move the old profile has no trace of it.
	if err := cmdSet([]string{filepath.Join(dstDir, "topic.json"),
		"movedFrom", configDir, "movedAt", nowISO()}); err != nil {
		return err
	}
	if carry && workDir != "" {
		if err := cmdCarryApprovals([]string{configFileOf(configDir), configFileOf(dst), workDir}); err != nil {
			return err
		}
	}

	// A moved topic must present itself as belonging where it now lives.
	if !keepBridge {
		sid := scalar(load(filepath.Join(dstDir, "topic.json"))["sessionId"])
		if moved := transcriptOf(dst, sid); moved != "" {
			dropped, err := dropBridge(moved, filepath.Join(dstDir, "backups"))
			if err != nil {
				return err
			}
			if dropped != "" {
				label := profileTag(dst)
				if label == "" {
					label = "default"
				}
				fmt.Printf("  remote control: dropped bridge %s… — it will re-register in %s's account\n",
					truncate(dropped, 16), label)
			}
		}
	}

	tagDst := profileTag(dst)
	if wasUp && !stayDown {
		fmt.Println()
		fmt.Printf("Bringing %q back up in %s\n", name, dst)
		if err := cmdUp([]string{name, dst, filepath.Join(dst, "topics")}); err != nil {
			// The move itself succeeded — say so plainly rather than letting a failed
			// restart read as a failed move.
			fmt.Println()
			fmt.Printf("%q MOVED to %s but could not be started there (see above).\n", name, dst)
			fmt.Println("  Nothing is lost: it is down and resumable. Once the blocker is cleared:")
			fmt.Printf("    CLAUDE_CONFIG_DIR=%s topic up %q\n", dst, name)
			return err
		}
		label := tagDst
		if label == "" {
			label = "default"
		}
		fmt.Println()
		fmt.Printf("Moved and running. It is now a %s-profile topic:\n", label)
		fmt.Printf("  attach:  tmux attach -t %q\n", tmuxName(tagDst, name))
		fmt.Printf("  manage:  CLAUDE_CONFIG_DIR=%s topic <cmd> %q\n", dst, name)
		return nil
	}

	fmt.Println()
	fmt.Println("Done. Pick it up in its new profile with:")
	if tagDst != "" {
		fmt.Printf("  CLAUDE_CONFIG_DIR=%s topic up %q\n", dst, name)
		fmt.Printf("  (its tmux session will be %q)\n", tmuxName(tagDst, name))
	} else {
		fmt.Printf("  topic up %q\n", name)
	}
	if copyMode {
		fmt.Printf("  The original is still in %s — resuming both would fork the transcript.\n", configDir)
	}
	return nil
}

// sessionIDsOf lists every session a topic can still resume: the current one and every
// retired one in history.
func sessionIDsOf(file string) []string {
	m := load(file)
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	add(scalar(m["sessionId"]))
	if h, ok := m["history"].([]any); ok {
		for _, e := range h {
			if em, ok := e.(map[string]any); ok {
				add(scalar(em["sessionId"]))
			}
		}
	}
	return out
}

//	rename <old> <new> <config-dir> <topics-root> [--dry-run]
//
// A SESSION carries its name in five places — tmux, --name, --remote-control, the
// transcript's custom-title, and the registry — and nothing keeps them in step;
// /rename in the Claude UI, for instance, changes only the cloud one. Keeping them
// consistent is what layering a topic over a session buys, and this is where that is
// paid for.
func cmdRename(args []string) error {
	dry := false
	var positional []string
	for _, a := range args {
		if a == "--dry-run" {
			dry = true
			continue
		}
		positional = append(positional, a)
	}
	if len(positional) < 4 {
		return fmt.Errorf(`usage: topic rename "<old>" "<new>" [--dry-run]`)
	}
	old, newName := positional[0], positional[1]
	configDir, topicsRoot := positional[len(positional)-2], positional[len(positional)-1]
	if old == newName {
		return fmt.Errorf("rename: the names are identical")
	}

	oldDir, newDir := topicDir(topicsRoot, old), topicDir(topicsRoot, newName)
	if !fileExists(filepath.Join(oldDir, "topic.json")) {
		return fmt.Errorf("no such topic in this profile: %q", old)
	}
	if oldDir != newDir && fileExists(filepath.Join(newDir, "topic.json")) {
		return fmt.Errorf("rename: %q already exists in this profile (%s)", newName, newDir)
	}
	if self := whoami(topicsRoot, configDir); self != "" && self == old {
		return fmt.Errorf("rename: %q is the session running this command — run it from another session", old)
	}
	if regGet(topicsRoot, old, "state") == "adopted" {
		apid, asid := regGet(topicsRoot, old, "adoptedPid"), regGet(topicsRoot, old, "sessionId")
		if apid != "" && liveSessionRecord(configDir, apid, asid) {
			return fmt.Errorf("rename: %q is adopted and still running as pid %s — exit it first", old, apid)
		}
	}

	tag := profileTag(configDir)
	sid := regGet(topicsRoot, old, "sessionId")
	tr := transcriptOf(configDir, sid)
	wasUp := isUp(tag, old)

	fmt.Printf("Renaming %q -> %q\n", old, newName)
	fmt.Printf("  registry:  %s -> %s\n", oldDir, newDir)
	fmt.Printf("  tmux:      %s -> %s\n", tmuxName(tag, old), tmuxName(tag, newName))
	if tr != "" {
		fmt.Println("  transcript: re-titled, and its Remote Control bridge re-minted")
	}
	fmt.Println("  NOTE: the Claude UI conversation restarts; the old entry is orphaned there.")
	if wasUp {
		fmt.Println("  it is UP: will be put down and brought back up under the new name")
	}
	if dry {
		fmt.Println()
		fmt.Println("Dry run — nothing changed.")
		return nil
	}

	if wasUp {
		if err := cmdDown([]string{old, configDir, topicsRoot}); err != nil {
			return err
		}
		if tr != "" {
			settle(tr)
		}
	}

	if oldDir != newDir {
		if err := os.MkdirAll(filepath.Dir(newDir), 0o755); err != nil {
			return err
		}
		if err := moveDir(oldDir, newDir); err != nil {
			return err
		}
	}
	if err := cmdSet([]string{filepath.Join(newDir, "topic.json"),
		"name", newName, "renamedFrom", old, "renamedAt", nowISO()}); err != nil {
		return err
	}

	if tr != "" {
		// Re-mint the bridge so --remote-control re-registers under the new name...
		if _, err := dropBridge(tr, filepath.Join(newDir, "backups")); err != nil {
			return err
		}
		// ...and re-title the transcript, so `adopt` can find it by the new name later.
		if err := cmdAppendTitle([]string{tr, newName, sid}); err != nil {
			return err
		}
	}

	// Lineage recorded by `fork` points at names; leave no dangling references.
	for _, n := range topicNames(topicsRoot) {
		out := captureRelink(topicFile(topicsRoot, n), old, newName)
		if out != "" {
			fmt.Printf("  updated lineage in %q\n", out)
		}
	}

	if wasUp {
		if err := cmdUp([]string{newName, configDir, topicsRoot}); err != nil {
			return err
		}
		fmt.Println()
		fmt.Printf("Renamed and running as %q.\n", newName)
		return nil
	}
	fmt.Println()
	fmt.Printf("Renamed. It is down; `topic up %q` starts it under the new name.\n", newName)
	return nil
}

// captureRelink applies a lineage rename and returns the affected topic's name.
func captureRelink(file, old, newName string) string {
	m := load(file)
	hit := false
	if scalar(m["forkedFrom"]) == old {
		m["forkedFrom"] = newName
		hit = true
	}
	if forks, ok := m["forks"].([]any); ok {
		for i, f := range forks {
			if scalar(f) == old {
				forks[i] = newName
				hit = true
			}
		}
		if hit {
			m["forks"] = forks
		}
	}
	if !hit {
		return ""
	}
	if save(file, m) != nil {
		return ""
	}
	return scalar(m["name"])
}
