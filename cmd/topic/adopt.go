package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// adopt brings a session `topic` did not launch under management. Four paths, in
// order of preference:
//
//	live record   — ~/.claude/sessions/<pid>.json already knows name -> uuid -> cwd
//	resumable     — the session exited; match the LAST custom-title in its transcript
//	--session-id  — the caller already knows the uuid (a session adopting itself)
//	--probe       — running but unnamed: nothing to match on in either direction

type liveSession struct{ pid, sid, cwd, name string }

func liveSessions(configDir string) []liveSession {
	paths, _ := filepath.Glob(filepath.Join(configDir, "sessions", "*.json"))
	sort.Strings(paths)
	var out []liveSession
	for _, p := range paths {
		d := load(p)
		pid, sid := scalar(d["pid"]), scalar(d["sessionId"])
		if pid == "" || sid == "" {
			continue
		}
		if n := atoi(pid); n == 0 || !alive(n) {
			continue
		}
		out = append(out, liveSession{pid, sid, scalar(d["cwd"]), scalar(d["name"])})
	}
	return out
}

type adoptOpts struct {
	name, probe, dir, exclude, pid, title, sessionID string
	dry                                              bool
	configDir, topicsRoot                            string
}

func (o adoptOpts) register(sid, dir, pid, note string) error {
	if o.dry {
		fmt.Printf("would adopt %q -> session %s… (%s, dir: %s)\n", o.name, truncate(sid, 8), note, dir)
		return nil
	}
	fields := []string{topicFile(o.topicsRoot, o.name),
		"name", o.name, "dir", dir, "sessionId", sid, "generation", "1", "adoptedAt", nowISO()}
	if pid != "" {
		fields = append(fields, "state", "adopted", "adoptedPid", pid)
	} else {
		// Nothing is running, so there is nothing to claim: an ordinary down topic.
		fields = append(fields, "state", "down")
	}
	return cmdSet(fields)
}

func cmdAdopt(args []string) error {
	o := adoptOpts{}
	var positional []string
	for i := 0; i < len(args); i++ {
		take := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch args[i] {
		case "--probe":
			o.probe = take()
		case "--dir":
			o.dir = take()
		case "--pid":
			o.pid = take()
		case "--title":
			o.title = take()
		case "--exclude":
			o.exclude = take()
		case "--session-id":
			o.sessionID = take()
		case "--dry-run":
			o.dry = true
		default:
			positional = append(positional, args[i])
		}
	}
	if len(positional) < 3 {
		return fmt.Errorf(`usage: topic adopt "<name>" [--pid <pid>] [--dir <dir>] | --probe <token>`)
	}
	o.name = positional[0]
	o.configDir, o.topicsRoot = positional[len(positional)-2], positional[len(positional)-1]

	// ---- a uuid the caller already knows -----------------------------------------
	if o.sessionID != "" {
		pid := ""
		for _, s := range liveSessions(o.configDir) {
			if s.sid == o.sessionID {
				pid = s.pid
			}
		}
		dir := o.dir
		if dir == "" {
			// The transcript records its own cwd; fall back to $PWD only if it has none.
			if tr := transcriptOf(o.configDir, o.sessionID); tr != "" {
				dir, _ = scanTranscript(tr)
			}
			if dir == "" {
				dir, _ = os.Getwd()
			}
		}
		note := "resumable, not running"
		if pid != "" {
			note = "running, pid " + pid
		}
		if err := o.register(o.sessionID, dir, pid, note); err != nil || o.dry {
			return err
		}
		if pid != "" {
			fmt.Printf("Adopted %q -> session %s… (pid %s, dir: %s)\n", o.name, truncate(o.sessionID, 8), pid, dir)
			fmt.Printf("  Still running OUTSIDE topic management. Exit it, then: topic up %q\n", o.name)
		} else {
			fmt.Printf("Adopted %q -> session %s… (resumable, dir: %s)\n", o.name, truncate(o.sessionID, 8), dir)
			fmt.Printf("  Not running, so nothing to claim. Pick it up with: topic up %q\n", o.name)
		}
		return nil
	}

	if o.probe != "" {
		return o.adoptByProbe()
	}

	// ---- preferred: the live session already published its own uuid ---------------
	var all []liveSession
	for _, s := range liveSessions(o.configDir) {
		if o.exclude != "" && s.sid == o.exclude {
			continue
		}
		all = append(all, s)
	}
	if len(all) == 0 {
		return o.adoptResumable(false)
	}

	rows := all
	narrow := func(match func(liveSession) bool) {
		var kept []liveSession
		for _, s := range rows {
			if match(s) {
				kept = append(kept, s)
			}
		}
		// A filter that matches nothing is ignored, not fatal — keep the wider set and
		// let the caller disambiguate.
		if len(kept) > 0 {
			rows = kept
		}
	}
	// --pid is an explicit assertion: if it matches no live session, say so rather
	// than quietly adopting something else.
	if o.pid != "" {
		narrow(func(s liveSession) bool { return s.pid == o.pid })
		if len(rows) != 1 || rows[0].pid != o.pid {
			return fmt.Errorf("adopt: no live session with pid %s (see %s/sessions)", o.pid, o.configDir)
		}
	}
	if o.dir != "" {
		narrow(func(s liveSession) bool { return s.cwd == o.dir })
	}
	narrow(func(s liveSession) bool { return s.name == o.name })

	if len(rows) != 1 {
		// Live sessions exist but none is ours — the target is probably a resumable
		// session that has exited. Try that before making the user disambiguate.
		if err := o.adoptResumable(true); err == nil {
			return nil
		}
		if len(rows) == len(all) && o.pid == "" && o.dir == "" {
			fmt.Fprintf(os.Stderr, "adopt: no live session named %q, and no resumable session titled\n", o.name)
			fmt.Fprintf(os.Stderr, "  %q. Pick a live one with --pid, or --title if it is\n", cmp(o.title, o.name))
			fmt.Fprintln(os.Stderr, "  listed under a different name in `claude --resume`:")
		} else {
			fmt.Fprintf(os.Stderr, "adopt: %d live sessions match — disambiguate with --pid (or --dir):\n", len(rows))
		}
		for _, s := range rows {
			fmt.Fprintf(os.Stderr, "  --pid %-7s %-40s %s\n", s.pid, s.name, s.cwd)
		}
		return fmt.Errorf("expected exactly one match")
	}

	s := rows[0]
	dir := o.dir
	if dir == "" {
		dir = s.cwd
	}
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}
	if err := o.register(s.sid, dir, s.pid,
		fmt.Sprintf("running, pid %s, advertised as %q", s.pid, s.name)); err != nil || o.dry {
		return err
	}
	fmt.Printf("Adopted %q -> session %s… (pid %s, advertised as %q, dir: %s)\n",
		o.name, truncate(s.sid, 8), s.pid, s.name, dir)
	fmt.Printf("  Still running OUTSIDE topic management as pid %s.\n", s.pid)
	fmt.Printf("  Exit that session, then: topic up %q\n", o.name)
	fmt.Println("  (no --claim needed — the pid is on record, so topic checks it for you)")
	return nil
}

func cmp(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// adoptResumable matches a session that has EXITED against the titles in transcripts.
// quiet lets the live path fall through to it speculatively.
func (o adoptOpts) adoptResumable(quiet bool) error {
	title := cmp(o.title, o.name)
	var rows []titledRow
	for _, line := range findTitled(o.configDir, title) {
		if o.dir != "" && line.cwd != o.dir {
			continue
		}
		rows = append(rows, line)
	}
	if len(rows) == 0 {
		if quiet {
			return fmt.Errorf("no match")
		}
		fmt.Fprintf(os.Stderr, "adopt: no live session and no resumable session titled %q.\n", title)
		fmt.Fprintln(os.Stderr, "  Titles come from the session's own transcript — check `claude --resume`.")
		fmt.Fprintf(os.Stderr, "  If it is named differently there: topic adopt %q --title \"<that name>\"\n", o.name)
		return fmt.Errorf("nothing to adopt")
	}
	if len(rows) > 1 {
		fmt.Fprintf(os.Stderr, "adopt: %d resumable sessions are titled %q — pick one:\n", len(rows), title)
		for _, r := range rows {
			fmt.Fprintf(os.Stderr, "  --session-id %s  last active %s  %s\n", r.uuid, r.mod, r.cwd)
		}
		return fmt.Errorf("expected exactly one match (newest listed first)")
	}

	r := rows[0]
	// It should not be running — the live path would have caught it — but the title
	// may differ from the advertised name, so check the uuid before calling it down.
	for _, s := range liveSessions(o.configDir) {
		if s.sid == r.uuid {
			return fmt.Errorf("adopt: session %s… is currently RUNNING — adopt it live: topic adopt %q --pid %s",
				truncate(r.uuid, 8), o.name, s.pid)
		}
	}
	dir := o.dir
	if dir == "" {
		dir = r.cwd
	}
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}
	if err := o.register(r.uuid, dir, "", fmt.Sprintf("resumable, titled %q", title)); err != nil || o.dry {
		return err
	}
	fmt.Printf("Adopted %q -> session %s… (resumable, titled %q, dir: %s)\n",
		o.name, truncate(r.uuid, 8), title, dir)
	fmt.Println("  Not running, so nothing to claim. Pick it up with:")
	fmt.Printf("    topic up %q\n", o.name)
	return nil
}

// adoptByProbe covers a session that is running but unnamed. A running session does
// not hold its transcript open, so the caller sends it a unique token and we find
// which transcript it landed in — excluding the caller's own, which also contains it.
func (o adoptOpts) adoptByProbe() error {
	paths, _ := filepath.Glob(filepath.Join(o.configDir, "projects", "*", "*.jsonl"))
	var hits []string
	for _, p := range paths {
		uuid := strings.TrimSuffix(filepath.Base(p), ".jsonl")
		if o.exclude != "" && uuid == o.exclude {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil || !strings.Contains(string(b), o.probe) {
			continue
		}
		hits = append(hits, p)
	}
	switch len(hits) {
	case 0:
		return fmt.Errorf("adopt: probe token %q not found in any transcript — did the target session receive it?", o.probe)
	case 1:
	default:
		fmt.Fprintf(os.Stderr, "adopt: probe matched %d transcripts — cannot disambiguate:\n", len(hits))
		for _, h := range hits {
			fmt.Fprintf(os.Stderr, "  %s\n", h)
		}
		return fmt.Errorf("re-run with --exclude <your-own-session-uuid>")
	}

	uuid := strings.TrimSuffix(filepath.Base(hits[0]), ".jsonl")
	dir := o.dir
	if dir == "" {
		dir, _ = scanTranscript(hits[0])
	}
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}
	if err := o.register(uuid, dir, "", "probe match"); err != nil || o.dry {
		return err
	}
	fmt.Printf("Adopted %q -> session %s… (dir: %s)\n", o.name, truncate(uuid, 8), dir)
	fmt.Println("  It is still running OUTSIDE topic management (no tmux, no --name).")
	fmt.Printf("  `topic down %q` then `topic up %q` brings it fully under management.\n", o.name, o.name)
	return nil
}
