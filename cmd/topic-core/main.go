// topic-core is the JSON layer behind `topic`.
//
// It exists because that layer was 23 inline `python3 -c` blocks: code embedded in
// shell strings, where a stray quote is a runtime bug and nothing is testable on its
// own. Everything here is state manipulation — no tmux, no claude, no orchestration —
// which makes it the natural first piece to move out of bash.
//
// Every subcommand is deliberately small and total: it either does the thing and
// exits 0, or explains itself on stderr and exits non-zero. Empty output plus exit 0
// is a legitimate answer ("no such key"), because that is what the shell callers
// expect from the substitutions they replaced.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fail("usage: topic-core <command> [args...]")
	}
	args := os.Args[2:]
	var err error
	switch os.Args[1] {
	case "get":
		err = cmdGet(args)
	case "set":
		err = cmdSet(args)
	case "push-history":
		err = cmdPushHistory(args)
	case "push-fork":
		err = cmdPushFork(args)
	case "relink":
		err = cmdRelink(args)
	case "status":
		err = cmdStatus(args)
	case "list-topics":
		err = cmdList(args)
	case "show":
		err = cmdStatusCmd(args)
	case "profiles":
		err = cmdProfiles(args)
	case "forget":
		err = cmdForget(args)
	case "prune":
		err = cmdPrune(args)
	case "rebridge":
		err = cmdRebridge(args)
	case "up":
		err = cmdUp(args)
	case "down":
		err = cmdDown(args)
	case "down-all":
		err = cmdDownAll(args)
	case "whoami":
		err = cmdWhoami(args)
	case "path":
		err = cmdPath(args)
	case "ensure-dispatcher":
		err = cmdEnsureDispatcher(args)
	case "fork":
		err = cmdFork(args)
	case "handoff-swap":
		err = cmdHandoffSwap(args)
	case "session-ids":
		err = cmdSessionIDs(args)
	case "project-flag":
		err = cmdProjectFlag(args)
	case "onboarded":
		err = cmdOnboarded(args)
	case "carry-approvals":
		err = cmdCarryApprovals(args)
	case "sessions":
		err = cmdSessions(args)
	case "transcript-cwd":
		err = cmdTranscriptCwd(args)
	case "transcript-title":
		err = cmdTranscriptTitle(args)
	case "strip-bridge":
		err = cmdStripBridge(args)
	case "find-titled":
		err = cmdFindTitled(args)
	case "append-title":
		err = cmdAppendTitle(args)
	default:
		fail("unknown command: %s", os.Args[1])
	}
	if err != nil {
		fail("%v", err)
	}
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "topic-core: "+format+"\n", a...)
	os.Exit(1)
}

func need(args []string, n int, usage string) {
	if len(args) < n {
		fail("usage: topic-core %s", usage)
	}
}

// ---------------------------------------------------------------- JSON documents

// load reads a JSON object, treating an unreadable or malformed file as empty. The
// bash it replaces did the same: a half-written registry entry must not be fatal to
// a command that is about to overwrite it.
func load(path string) map[string]any {
	m := map[string]any{}
	b, err := os.ReadFile(path)
	if err != nil {
		return m
	}
	_ = json.Unmarshal(b, &m)
	return m
}

// save writes atomically: a torn registry entry is worse than a failed command.
func save(path string, m map[string]any) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// scalar renders a value the way the shell wants it: bare text, no JSON quoting, and
// integers without a float's decimal tail.
func scalar(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "True" // matches what the callers already compare against
		}
		return "False"
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

func cmdGet(args []string) error {
	need(args, 2, "get <file> <key>")
	fmt.Println(scalar(load(args[0])[args[1]]))
	return nil
}

func cmdSet(args []string) error {
	need(args, 3, "set <file> <key> <value> [<key> <value>...]")
	path, kv := args[0], args[1:]
	if len(kv)%2 != 0 {
		return fmt.Errorf("set: odd number of key/value arguments (%d)", len(kv))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	m := load(path)
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return save(path, m)
}

func cmdPushHistory(args []string) error {
	need(args, 3, "push-history <file> <sessionId> <handoffDoc>")
	m := load(args[0])
	hist, _ := m["history"].([]any)
	m["history"] = append(hist, map[string]any{
		"sessionId":  args[1],
		"handoffDoc": args[2],
		"retiredAt":  time.Now().Format("2006-01-02T15:04:05-07:00"),
	})
	return save(args[0], m)
}

func cmdPushFork(args []string) error {
	need(args, 2, "push-fork <file> <child>")
	if _, err := os.Stat(args[0]); err != nil {
		return nil // parent not registered: nothing to record on
	}
	m := load(args[0])
	forks, _ := m["forks"].([]any)
	for _, f := range forks {
		if scalar(f) == args[1] {
			return nil
		}
	}
	m["forks"] = append(forks, args[1])
	return save(args[0], m)
}

// relink repoints lineage after a rename, so `fork` references never dangle. Prints
// the topic's name when it changed something, and nothing when it did not.
func cmdRelink(args []string) error {
	need(args, 3, "relink <file> <old> <new>")
	path, old, new := args[0], args[1], args[2]
	m := load(path)
	hit := false
	if scalar(m["forkedFrom"]) == old {
		m["forkedFrom"] = new
		hit = true
	}
	if forks, ok := m["forks"].([]any); ok {
		for i, f := range forks {
			if scalar(f) == old {
				forks[i] = new
				hit = true
			}
		}
		if hit {
			m["forks"] = forks
		}
	}
	if !hit {
		return nil
	}
	if err := save(path, m); err != nil {
		return err
	}
	fmt.Println(scalar(m["name"]))
	return nil
}

func cmdStatus(args []string) error {
	need(args, 1, "status <file>")
	m := load(args[0])
	for _, k := range []string{"dir", "sessionId", "generation", "forkedFrom", "seededFrom",
		"movedFrom", "movedAt", "renamedFrom", "renamedAt", "lastPickedUp", "lastPutDown", "lastHandoff"} {
		if v := scalar(m[k]); v != "" {
			fmt.Printf("  %s: %s\n", k, v)
		}
	}
	if forks, ok := m["forks"].([]any); ok && len(forks) > 0 {
		names := make([]string, 0, len(forks))
		for _, f := range forks {
			names = append(names, scalar(f))
		}
		fmt.Printf("  forks: %s\n", strings.Join(names, ", "))
	}
	if h, ok := m["history"].([]any); ok && len(h) > 0 {
		fmt.Printf("  retired sessions: %d\n", len(h))
	}
	return nil
}

// sessionIDs lists every session this topic can still resume: the current one and
// every retired one. Order matters — the current session is first.
func cmdSessionIDs(args []string) error {
	need(args, 1, "session-ids <file>")
	m := load(args[0])
	seen := map[string]bool{}
	emit := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			fmt.Println(s)
		}
	}
	emit(scalar(m["sessionId"]))
	if h, ok := m["history"].([]any); ok {
		for _, e := range h {
			if em, ok := e.(map[string]any); ok {
				emit(scalar(em["sessionId"]))
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------- profile configs

func projectEntry(cfg map[string]any, dir string) map[string]any {
	projects, _ := cfg["projects"].(map[string]any)
	entry, _ := projects[dir].(map[string]any)
	return entry
}

func cmdProjectFlag(args []string) error {
	need(args, 3, "project-flag <config-file> <work-dir> <key>")
	fmt.Println(scalar(projectEntry(load(args[0]), args[1])[args[2]]))
	return nil
}

// onboarded reports through its exit status, since that is how the shell asks.
func cmdOnboarded(args []string) error {
	need(args, 1, "onboarded <config-file>")
	if load(args[0])["hasCompletedOnboarding"] == true {
		os.Exit(0)
	}
	os.Exit(1)
	return nil
}

// carryApprovals copies the user's own prior answers for one directory. Limited on
// purpose to the two approval flags: the same entry holds allowedTools, and copying a
// permission allowlist into a profile that never granted it is a silent escalation.
func cmdCarryApprovals(args []string) error {
	need(args, 3, "carry-approvals <src-config> <dst-config> <work-dir>")
	src, dst, dir := args[0], args[1], args[2]
	entry := projectEntry(load(src), dir)
	if entry == nil {
		return nil
	}
	carried := map[string]any{}
	for _, k := range []string{"hasTrustDialogAccepted", "hasClaudeMdExternalIncludesApproved"} {
		if v, ok := entry[k]; ok {
			carried[k] = v
		}
	}
	if len(carried) == 0 {
		return nil
	}
	m := load(dst)
	projects, _ := m["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
	}
	target, _ := projects[dir].(map[string]any)
	if target == nil {
		target = map[string]any{}
	}
	keys := make([]string, 0, len(carried))
	for k, v := range carried {
		target[k] = v
		keys = append(keys, fmt.Sprintf("%s=%s", k, scalar(v)))
	}
	sort.Strings(keys)
	projects[dir] = target
	m["projects"] = projects
	if err := save(dst, m); err != nil {
		return err
	}
	fmt.Printf("  carried approvals: %s\n", strings.Join(keys, ", "))
	return nil
}

// sessions lists this profile's LIVE sessions as pid\tsessionId\tcwd\tname. Records
// are not removed when a session exits, so liveness is re-checked against the pid.
func cmdSessions(args []string) error {
	need(args, 1, "sessions <config-dir>")
	paths, _ := filepath.Glob(filepath.Join(args[0], "sessions", "*.json"))
	sort.Strings(paths)
	for _, p := range paths {
		d := load(p)
		pid, sid := scalar(d["pid"]), scalar(d["sessionId"])
		if pid == "" || sid == "" {
			continue
		}
		n, err := strconv.Atoi(pid)
		if err != nil || !alive(n) {
			continue
		}
		fmt.Printf("%s\t%s\t%s\t%s\n", pid, sid, scalar(d["cwd"]), scalar(d["name"]))
	}
	return nil
}

func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// ---------------------------------------------------------------- transcripts

// scanTranscript walks a .jsonl once, returning the FIRST cwd and the LAST
// custom-title. Last wins for the title because a rename appends another record; an
// earlier one is a name since abandoned.
func scanTranscript(path string) (cwd, title string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024) // transcripts have long lines
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		hasTitle := strings.Contains(string(line), `"custom-title"`)
		hasCwd := cwd == "" && strings.Contains(string(line), `"cwd"`)
		if !hasTitle && !hasCwd {
			continue
		}
		var rec map[string]any
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		if hasTitle && scalar(rec["type"]) == "custom-title" {
			title = scalar(rec["customTitle"])
		}
		if hasCwd {
			if c := scalar(rec["cwd"]); c != "" {
				cwd = c
			}
		}
	}
	return cwd, title
}

func cmdTranscriptCwd(args []string) error {
	need(args, 1, "transcript-cwd <transcript>")
	cwd, _ := scanTranscript(args[0])
	fmt.Println(cwd)
	return nil
}

func cmdTranscriptTitle(args []string) error {
	need(args, 1, "transcript-title <transcript>")
	_, title := scanTranscript(args[0])
	fmt.Println(title)
	return nil
}

// stripBridge removes the bridge-session records so the next launch mints a fresh
// Remote Control identity. Prints the bridge it dropped, or nothing if there was none.
func cmdStripBridge(args []string) error {
	need(args, 1, "strip-bridge <transcript>")
	path := args[0]
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var kept []string
	last := ""
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, `"bridge-session"`) {
			var rec map[string]any
			if json.Unmarshal([]byte(line), &rec) == nil && scalar(rec["type"]) == "bridge-session" {
				if id := scalar(rec["bridgeSessionId"]); id != "" {
					last = id
				}
				continue
			}
		}
		kept = append(kept, line)
	}
	f.Close()
	if last == "" {
		return nil // nothing to strip; leave the file untouched
	}
	tmp := path + ".tmp"
	out := strings.Join(kept, "\n")
	if out != "" {
		out += "\n"
	}
	if err := os.WriteFile(tmp, []byte(out), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	fmt.Println(last)
	return nil
}

// findTitled locates resumable sessions by the name `claude --resume` shows, which is
// the LAST custom-title in a transcript. Emits uuid\tcwd\tlast-modified, newest first.
//
// This scans every transcript in the profile, so it runs the candidate filter in
// parallel: the corpus is routinely hundreds of megabytes and this is on the path of
// every `topic adopt`.
func cmdFindTitled(args []string) error {
	need(args, 2, "find-titled <config-dir> <title>")
	dir, want := args[0], args[1]
	paths, _ := filepath.Glob(filepath.Join(dir, "projects", "*", "*.jsonl"))
	needle := []byte(`"customTitle":"` + want + `"`)

	type row struct {
		uuid, cwd string
		mod       time.Time
	}
	results := make(chan row, len(paths))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, p := range paths {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			b, err := os.ReadFile(p)
			if err != nil || !bytes.Contains(b, needle) {
				return
			}
			// Contains the name somewhere, but a rename appends: only the LAST
			// title counts, or a session renamed away would still match.
			cwd, title := scanTranscript(p)
			if title != want {
				return
			}
			st, err := os.Stat(p)
			if err != nil {
				return
			}
			results <- row{strings.TrimSuffix(filepath.Base(p), ".jsonl"), cwd, st.ModTime()}
		}(p)
	}
	wg.Wait()
	close(results)

	var rows []row
	for r := range results {
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].mod.After(rows[j].mod) })
	for _, r := range rows {
		fmt.Printf("%s\t%s\t%s\n", r.uuid, r.cwd, r.mod.Format("2006-01-02 15:04"))
	}
	return nil
}

// appendTitle re-titles a transcript. A rename appends rather than rewrites, so the
// file keeps its history of names and the last one is current.
func cmdAppendTitle(args []string) error {
	need(args, 3, "append-title <transcript> <title> <sessionId>")
	f, err := os.OpenFile(args[0], os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(map[string]any{
		"type": "custom-title", "customTitle": args[1], "sessionId": args[2],
	})
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}
