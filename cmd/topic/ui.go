package main

// `topic ui`: the topic list on the left, the selected topic's tmux session on the right.
//
// Three modes of one command share this file:
//
//	topic ui            build the layout (once) and attach to it
//	topic ui --rows     the grouped rows the picker shows (also fzf's ctrl-r reload)
//	topic ui --pick     the picker itself, which `topic ui` runs in the left pane
//
// The layout lives on its own tmux server (-L), so it nests over the topic sessions
// without a prefix clash: the UI answers to C-a, and inside the right pane C-b still
// drives the topic's own tmux. The picker talks to two servers — its own, to move the
// right pane, and the default one, where the topic sessions live — which is why it
// unsets $TMUX before doing anything else.

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// uiSocket names the tmux server that holds the layout. TOPIC_UI_SOCKET overrides it
// so tests do not collide with a UI the developer has open.
func uiSocket() string {
	if s := os.Getenv("TOPIC_UI_SOCKET"); s != "" {
		return s
	}
	return uiSocketPrefix
}

// Every plain `topic ui` is its own instance: a tmux server whose socket is
// uiSocketPrefix-<id>. Two terminal windows are two UIs, each with its own folded groups.
// TOPIC_UI_SOCKET pins one fixed name instead (tests use it, and so does the picker, which
// inherits its instance from the environment of the server that runs it).
const uiSocketPrefix = "topics-ui"

// uiSocketDir is where tmux keeps its sockets: $TMUX_TMPDIR (or /tmp) / tmux-<uid>.
func uiSocketDir() string {
	dir := os.Getenv("TMUX_TMPDIR")
	if dir == "" {
		dir = "/tmp"
	}
	return filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()))
}

// uiInstances lists the running UI instances by socket name. A socket file whose server
// is gone is skipped.
func uiInstances() []string {
	entries, err := os.ReadDir(uiSocketDir())
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if n != uiSocketPrefix && !strings.HasPrefix(n, uiSocketPrefix+"-") {
			continue
		}
		if tmuxCmd("-L", n, "has-session").Run() == nil {
			names = append(names, n)
		} else {
			removeUISocket(n) // a closed instance's leftover socket file
		}
	}
	sort.Strings(names)
	return names
}

// uiSocketGrace is how old a socket file must be before it can be called abandoned. A new
// instance's tmux server creates its socket before it has a session, and a failed
// has-session in that window must not unlink a live server's only way in.
const uiSocketGrace = 3 * time.Second

// removeUISocket deletes the socket file a closed instance leaves behind, so --list and
// the socket directory stay clean. It only does so when the file is past uiSocketGrace
// and nothing is listening: connecting is refused. Anything else (a server that answers,
// one still starting, an unexpected error) is left alone.
func removeUISocket(name string) {
	path := filepath.Join(uiSocketDir(), name)
	fi, err := os.Lstat(path)
	if err != nil || time.Since(fi.ModTime()) < uiSocketGrace {
		return
	}
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err == nil {
		conn.Close() // a live server
		return
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		_ = os.Remove(path)
	}
}

// uiID is the short name of an instance: what --list prints and --attach/--quit accept.
func uiID(socket string) string {
	if socket == uiSocketPrefix { // an instance from before each launch got its own
		return socket
	}
	return strings.TrimPrefix(socket, uiSocketPrefix+"-")
}

// newUIInstance picks an unused socket name for a new instance.
func newUIInstance() string {
	for {
		var b [2]byte
		_, _ = rand.Read(b[:])
		name := fmt.Sprintf("%s-%x", uiSocketPrefix, b)
		if tmuxCmd("-L", name, "has-session").Run() != nil {
			return name
		}
	}
}

// resolveUIInstance finds the instance a command means. With an id it is that one; without
// one it is the instance the environment pins, or the only one running.
func resolveUIInstance(id string) (string, error) {
	live := uiInstances()
	if id != "" {
		for _, n := range live {
			if n == id || uiID(n) == id {
				return n, nil
			}
		}
		return "", fmt.Errorf("ui: no running instance %q (topic ui --list)", id)
	}
	if s := os.Getenv("TOPIC_UI_SOCKET"); s != "" {
		return s, nil
	}
	switch len(live) {
	case 0:
		return "", fmt.Errorf("the topic UI is not running")
	case 1:
		return live[0], nil
	}
	ids := make([]string, len(live))
	for i, n := range live {
		ids[i] = uiID(n)
	}
	return "", fmt.Errorf("ui: %d instances are running (%s): name one, or use --all", len(live), strings.Join(ids, ", "))
}

// uiTmux runs tmux against the UI's own server.
func uiTmux(args ...string) *exec.Cmd {
	return tmuxCmd(append([]string{"-L", uiSocket()}, args...)...)
}

// uiRow is one line of the picker: a topic, or a directory heading above its topics.
type uiRow struct {
	state string // "UP", "down", "ADOPTED", "SESSION" (a plain tmux session); empty for a heading
	label string // the topic name, or the directory heading
	dir   string // headings only: the full directory, which is what folding is keyed on
	// hidden is the number of topics a collapsed heading is hiding; zero when expanded.
	hidden int
}

// otherSessionsDir is the key (and heading) of the group holding tmux sessions that are
// not topics.
const otherSessionsDir = "other sessions"

// extraSessionPatterns reads <topicsRoot>/ui-sessions: one tmux session name or glob per
// line, '#' for comments. It lists what else the UI should show besides topics, such as a
// daemon's session. A missing file means none.
func extraSessionPatterns(topicsRoot string) []string {
	data, err := os.ReadFile(filepath.Join(topicsRoot, "ui-sessions"))
	if err != nil {
		return nil
	}
	var pats []string
	for _, l := range strings.Split(string(data), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			pats = append(pats, l)
		}
	}
	return pats
}

// addSessionPattern appends pattern to <topicsRoot>/ui-sessions. It reports false, and
// leaves the file alone, when the pattern is already listed.
func addSessionPattern(topicsRoot, pattern string) (bool, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false, fmt.Errorf("ui: --add-session needs a session name or pattern")
	}
	if _, err := filepath.Match(pattern, ""); err != nil {
		return false, fmt.Errorf("ui: bad pattern %q: %w", pattern, err)
	}
	for _, have := range extraSessionPatterns(topicsRoot) {
		if have == pattern {
			return false, nil
		}
	}
	path := filepath.Join(topicsRoot, "ui-sessions")
	existing, _ := os.ReadFile(path)
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		existing = append(existing, '\n')
	}
	if err := os.MkdirAll(topicsRoot, 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, append(existing, pattern+"\n"...), 0o644)
}

// removeSessionPattern deletes pattern's line from <topicsRoot>/ui-sessions, keeping
// comments and the other patterns. It reports false when the pattern was not listed.
func removeSessionPattern(topicsRoot, pattern string) (bool, error) {
	pattern = strings.TrimSpace(pattern)
	path := filepath.Join(topicsRoot, "ui-sessions")
	data, err := os.ReadFile(path)
	if err != nil {
		return false, nil // no file: nothing listed
	}
	var kept []string
	removed := false
	for _, l := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if strings.TrimSpace(l) == pattern && pattern != "" && !strings.HasPrefix(pattern, "#") {
			removed = true
			continue
		}
		kept = append(kept, l)
	}
	if !removed {
		return false, nil
	}
	out := ""
	if len(kept) > 0 {
		out = strings.Join(kept, "\n") + "\n"
	}
	return true, os.WriteFile(path, []byte(out), 0o644)
}

// matchSessions picks, from the live tmux session names, those a pattern names that are
// not topics' own sessions, in name order.
func matchSessions(live, patterns []string, isTopic func(string) bool) []string {
	var out []string
	for _, name := range live {
		if isTopic(name) {
			continue
		}
		for _, pat := range patterns {
			if ok, _ := filepath.Match(pat, name); ok {
				out = append(out, name)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// liveTmuxSessions lists the session names on the default tmux server.
func liveTmuxSessions() []string {
	out, err := tmuxCmd("list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}

// topicState is how a topic is shown: running, put down, or adopted. An adopted topic
// runs outside tmux, so isUp cannot see it; saying so beats calling a live session
// "down".
func topicState(tag, topicsRoot, name string) string {
	switch {
	case isUp(tag, name):
		return "UP"
	case scalar(load(filepath.Join(topicsRoot, slugify(name), "topic.json"))["state"]) == "adopted":
		return "ADOPTED"
	}
	return "down"
}

// shortDir renders a working directory for a narrow pane: home becomes ~, and a path
// still longer than max keeps its tail, because the end is what tells projects apart.
func shortDir(dir, home string, max int) string {
	if dir == "" {
		return "(no directory)"
	}
	if home != "" && (dir == home || strings.HasPrefix(dir, home+string(filepath.Separator))) {
		dir = "~" + strings.TrimPrefix(dir, home)
	}
	if r := []rune(dir); max > 1 && len(r) > max {
		tail := string(r[len(r)-(max-1):])
		if i := strings.IndexByte(tail, '/'); i >= 0 {
			tail = tail[i:] // start on a whole path component, not mid-word
		}
		dir = "…" + tail
	}
	return dir
}

// buildRows groups the registry's topics by working directory: a heading per directory
// (in path order), its topics beneath it (in name order). A directory in collapsed keeps
// its heading and loses its topics. state is injected so the grouping can be tested
// without tmux. sessions are non-topic tmux sessions, shown last in a group of their own.
func buildRows(topicsRoot, home string, maxDir int, collapsed map[string]bool, sessions []string, state func(name string) string) []uiRow {
	groups := map[string][]string{}
	for _, name := range topicNames(topicsRoot) {
		dir := scalar(load(filepath.Join(topicsRoot, slugify(name), "topic.json"))["dir"])
		groups[dir] = append(groups[dir], name)
	}
	dirs := make([]string, 0, len(groups))
	for d := range groups {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	var rows []uiRow
	for _, d := range dirs {
		names := groups[d]
		heading := uiRow{label: shortDir(d, home, maxDir), dir: d}
		if collapsed[d] {
			heading.hidden = len(names)
			rows = append(rows, heading)
			continue
		}
		rows = append(rows, heading)
		sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
		for _, n := range names {
			rows = append(rows, uiRow{state: state(n), label: n})
		}
	}
	if len(sessions) > 0 {
		heading := uiRow{label: otherSessionsDir, dir: otherSessionsDir}
		if collapsed[otherSessionsDir] {
			heading.hidden = len(sessions)
			return append(rows, heading)
		}
		rows = append(rows, heading)
		for _, n := range sessions {
			rows = append(rows, uiRow{state: "SESSION", label: n})
		}
	}
	return rows
}

// Picker rows are STATE<TAB>DISPLAY<TAB>LABEL. STATE and LABEL are what the picker parses
// on selection; only DISPLAY is shown (--with-nth=2). DISPLAY is a coloured arrow and the
// name in one field, because a tab between them would expand to eight columns of a pane
// that has thirty.
const (
	ansiGreen  = "\033[32m"
	ansiRed    = "\033[31m"
	ansiYellow = "\033[33m"
	ansiDim    = "\033[2m"
	ansiReset  = "\033[0m"
)

// colour wraps s in an ANSI colour unless the user asked for none (NO_COLOR).
func colour(code, s string) string {
	if os.Getenv("NO_COLOR") != "" {
		return s
	}
	return code + s + ansiReset
}

// mark is the status glyph: up and down arrows, and a diamond for a session that runs
// outside tmux, each in its own colour.
func (r uiRow) mark() string {
	switch r.state {
	case "UP":
		return colour(ansiGreen, "▲")
	case "down":
		return colour(ansiRed, "▼")
	case "ADOPTED":
		return colour(ansiYellow, "◆")
	case "SESSION":
		return colour(ansiGreen, "●")
	}
	return ""
}

func (r uiRow) line() string {
	if r.state == "" { // a directory heading
		// The heading's third field is the full directory: what enter and double-click
		// hand back, so the picker knows which group to fold.
		return "\t" + colour(ansiDim, r.headingText()) + "\t" + r.dir
	}
	return r.state + "\t" + r.mark() + " " + r.label + "\t" + r.label
}

// headingText is a directory heading with its fold marker: ▾ open, ▸ folded with the
// number of topics it hides.
func (r uiRow) headingText() string {
	if r.hidden > 0 {
		return fmt.Sprintf("▸ %s (%d)", r.label, r.hidden)
	}
	return "▾ " + r.label
}

func currentRows(p profile, collapsed map[string]bool) []uiRow {
	home, _ := os.UserHomeDir()
	tag := profileTag(p.configDir)
	isTopic := map[string]bool{}
	for _, n := range topicNames(p.topicsRoot) {
		isTopic[tmuxName(tag, n)] = true
	}
	var sessions []string
	if pats := extraSessionPatterns(p.topicsRoot); len(pats) > 0 {
		sessions = matchSessions(liveTmuxSessions(), pats, func(n string) bool { return isTopic[n] })
	}
	return buildRows(p.topicsRoot, home, 28, collapsed, sessions, func(name string) string {
		return topicState(tag, p.topicsRoot, name)
	})
}

// cmdUI dispatches the three modes.
func cmdUI(args []string, p profile) error {
	var rows, pick, noAttach, quit, attach, list, all bool
	var instance string
	collapsed := map[string]bool{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if dir, ok := strings.CutPrefix(a, "--collapse="); ok {
			collapsed[dir] = true
			continue
		}
		switch a {
		case "--remove-session":
			if i+1 >= len(args) {
				return fmt.Errorf("ui: --remove-session needs the session name or pattern as it was added, e.g. \"fabrik*\"")
			}
			pat := args[i+1]
			removed, err := removeSessionPattern(p.topicsRoot, pat)
			if err != nil {
				return err
			}
			if removed {
				fmt.Printf("removed %q\n", pat)
			} else {
				fmt.Printf("%q is not listed (see %s)\n", pat, filepath.Join(p.topicsRoot, "ui-sessions"))
			}
			return nil
		case "--add-session":
			if i+1 >= len(args) {
				return fmt.Errorf("ui: --add-session needs a session name or pattern, e.g. \"fabrik*\"")
			}
			pat := args[i+1]
			added, err := addSessionPattern(p.topicsRoot, pat)
			if err != nil {
				return err
			}
			if added {
				fmt.Printf("added %q: matching tmux sessions show under %q in topic ui\n", pat, otherSessionsDir)
			} else {
				fmt.Printf("%q is already listed\n", pat)
			}
			return nil
		case "--rows":
			rows = true
		case "--pick":
			pick = true
		case "--no-attach":
			noAttach = true
		case "--quit":
			quit = true
			instance = optionalValue(args, &i)
		case "--attach":
			attach = true
			instance = optionalValue(args, &i)
		case "--list":
			list = true
		case "--all":
			all = true
		default:
			return fmt.Errorf("ui: unknown option %q (try: topic ui --help)", a)
		}
	}
	switch {
	case rows:
		for _, r := range currentRows(p, collapsed) {
			fmt.Println(r.line())
		}
		return nil
	case pick:
		return runPicker(p, collapsed)
	case list:
		live := uiInstances()
		if len(live) == 0 {
			fmt.Println("the topic UI is not running")
		}
		for _, n := range live {
			state := "detached"
			if out, err := tmuxCmd("-L", n, "list-clients").Output(); err == nil && strings.TrimSpace(string(out)) != "" {
				state = "attached"
			}
			fmt.Printf("%s\t%s\n", uiID(n), state)
		}
		return nil
	case quit:
		// Only a UI's own server goes; the topics live on the default one and keep running.
		targets := []string{}
		if all {
			targets = uiInstances()
		} else {
			n, err := resolveUIInstance(instance)
			if err != nil {
				if instance == "" && len(uiInstances()) == 0 {
					fmt.Println(err)
					return nil
				}
				return err
			}
			targets = append(targets, n)
		}
		if len(targets) == 0 {
			fmt.Println("the topic UI is not running")
		}
		for _, n := range targets {
			if err := tmuxCmd("-L", n, "kill-server").Run(); err != nil {
				fmt.Printf("%s: not running\n", uiID(n))
			}
			removeUISocket(n)
		}
		return nil
	case attach:
		n, err := resolveUIInstance(instance)
		if err != nil {
			return err
		}
		os.Setenv("TOPIC_UI_SOCKET", n)
	case os.Getenv("TOPIC_UI_SOCKET") == "":
		uiInstances() // sweeps the sockets of instances that have closed
		os.Setenv("TOPIC_UI_SOCKET", newUIInstance())
	}
	return openUI(p, noAttach)
}

// optionalValue returns the argument after args[*i] when it is not itself an option, and
// steps *i past it.
func optionalValue(args []string, i *int) string {
	if *i+1 < len(args) && !strings.HasPrefix(args[*i+1], "-") {
		*i++
		return args[*i]
	}
	return ""
}

// openUI builds the layout if it is not already up, then attaches to it.
func openUI(p profile, noAttach bool) error {
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("ui: tmux is not installed")
	}
	if uiTmux("has-session", "-t", "="+uiSocket()).Run() != nil {
		self, err := os.Executable()
		if err != nil {
			return err
		}
		// Address panes by id, never by index: base-index and pane-base-index vary with
		// the user's tmux.conf, and a hard-coded "0.1" silently misses.
		out, err := uiTmux("new-session", "-d", "-P", "-F", "#{pane_id}", "-s", uiSocket(),
			"-x", "220", "-y", "55", shellQuote(self)+" ui --pick").Output()
		if err != nil {
			return fmt.Errorf("ui: starting tmux: %w", err)
		}
		left := strings.TrimSpace(string(out))
		for _, opt := range [][]string{
			{"set", "-g", "prefix", "C-a"},
			{"bind", "C-a", "send-prefix"},
			{"set", "-g", "mouse", "on"},
			{"set", "-g", "remain-on-exit", "on"},
			{"set", "-g", "status-right", " C-a h: list | C-a l: session | C-a d: detach | C-a q: quit "},
			{"bind", "q", "confirm-before", "-p", "close the topic UI? (topics keep running) (y/n)", "kill-server"},
			// tmux shrinks the first pane first when a client is narrower than the window
			// was built for (a 54-column list became 4). main-vertical pins the list to a
			// fixed width and is reapplied whenever a client attaches or resizes.
			{"setw", "-g", "main-pane-width", "34"},
			{"set-hook", "-g", "client-attached", "select-layout main-vertical"},
			{"set-hook", "-g", "client-resized", "select-layout main-vertical"},
			{"bind", "h", "select-pane", "-L"},
			{"bind", "l", "select-pane", "-R"},
		} {
			if err := uiTmux(opt...).Run(); err != nil {
				return fmt.Errorf("ui: tmux %s: %w", opt[0], err)
			}
		}
		out, err = uiTmux("split-window", "-h", "-l", "75%", "-P", "-F", "#{pane_id}", "-t", left,
			"echo 'Select a topic on the left.'; exec "+shellQuote(userShell())).Output()
		if err != nil {
			return fmt.Errorf("ui: splitting the window: %w", err)
		}
		if err := uiTmux("set", "-g", "@right-pane", strings.TrimSpace(string(out))).Run(); err != nil {
			return err
		}
		if err := uiTmux("set", "-g", "@left-pane", left).Run(); err != nil {
			return err
		}
		_ = uiTmux("select-layout", "main-vertical").Run()
		_ = uiTmux("select-pane", "-t", left).Run()
	} else {
		revivePicker()
	}
	if noAttach {
		return nil
	}
	cmd := uiTmux("attach", "-t", "="+uiSocket())
	cmd.Env = withoutEnv(os.Environ(), "TMUX") // else tmux refuses to nest
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// revivePicker restarts the left pane's picker if it has exited. remain-on-exit keeps a
// dead pane on screen ("Pane is dead"), and reattaching to it would show that corpse for
// good.
func revivePicker() {
	out, err := uiTmux("show", "-gv", "@left-pane").Output()
	if err != nil {
		return // a UI built before the left pane was recorded
	}
	left := strings.TrimSpace(string(out))
	if dead, err := uiTmux("display-message", "-p", "-t", left, "#{pane_dead}").Output(); err == nil && strings.TrimSpace(string(dead)) == "1" {
		_ = uiTmux("respawn-pane", "-k", "-t", left).Run()
	}
}

// uiMessage flashes text in the UI's status line. display-message expands tmux formats
// (#{...}, #(cmd)) in its argument, and topic names and command output are user data, so
// every '#' is doubled to make it literal.
func uiMessage(text string) {
	_ = uiTmux("display-message", "-d", "5000", escapeTmuxFormat(text)).Run()
}

func escapeTmuxFormat(s string) string { return strings.ReplaceAll(s, "#", "##") }

// lastLine is the last non-empty line of a command's output, or the error if it said
// nothing: enough to explain a failure in one status line.
func lastLine(out string, err error) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if l := strings.TrimSpace(lines[len(lines)-1]); l != "" {
		return l
	}
	return err.Error()
}

// withoutEnv returns environ minus the named variable.
func withoutEnv(environ []string, name string) []string {
	var out []string
	for _, kv := range environ {
		if !strings.HasPrefix(kv, name+"=") {
			out = append(out, kv)
		}
	}
	return out
}

// binaryStamp identifies a version of the executable: a rebuild changes the mtime, and
// usually the size.
type binaryStamp struct {
	mod  time.Time
	size int64
}

func stampOf(path string) (binaryStamp, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return binaryStamp{}, false
	}
	return binaryStamp{fi.ModTime(), fi.Size()}, true
}

// watchBinary closes the returned channel once the file at path has changed and then held
// still for one poll. Waiting for it to settle matters: `make build` rewrites the binary
// and signs it in place, and exec'ing a half-written file would kill the picker.
func watchBinary(path string, every time.Duration) <-chan struct{} {
	changed := make(chan struct{})
	start, ok := stampOf(path)
	if !ok {
		return changed // cannot watch what cannot be seen: never fires
	}
	go func() {
		last := start
		for {
			time.Sleep(every)
			now, ok := stampOf(path)
			if !ok { // mid-replace: look again
				continue
			}
			if now == last && now != start {
				close(changed)
				return
			}
			last = now
		}
	}()
	return changed
}

// isDone reports whether ch has been closed, without blocking.
func isDone(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// reexecPicker replaces this process with the (new) binary running the picker, carrying
// the folded groups across so a rebuild does not unfold the list. It returns only if the
// exec failed, and the picker then carries on with the old code.
func reexecPicker(self string, collapsed map[string]bool) error {
	args := []string{self, "ui", "--pick"}
	var dirs []string
	for d, folded := range collapsed {
		if folded {
			dirs = append(dirs, d)
		}
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		args = append(args, "--collapse="+d)
	}
	return replaceProcess(self, args)
}

// runPicker is the left pane: choose a topic, point the right pane at it, repeat. When
// the topic binary is rebuilt underneath it, it re-executes itself, so a new build shows
// up without closing the UI.
func runPicker(p profile, collapsed map[string]bool) error {
	// This pane lives inside the UI's own tmux server, so $TMUX points there; topic's
	// own tmux calls must reach the default server, where the topic sessions are.
	os.Unsetenv("TMUX")
	// A stray Ctrl-C in this pane (or one meant for fzf) must not kill the picker and leave
	// "Pane is dead" behind. Notify, unlike Ignore, is reset in the fzf child, so fzf still
	// answers it.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt)
	go func() {
		for range sigs {
		}
	}()
	self, err := os.Executable()
	if err != nil {
		return err
	}
	tag := profileTag(p.configDir)
	stale := watchBinary(self, time.Second)
	cursor := 0 // the row to land on, so folding a group does not throw the cursor to the top
	for {
		if isDone(stale) {
			if err := reexecPicker(self, collapsed); err != nil {
				uiMessage("could not reload the topic UI: " + err.Error())
				stale = make(chan struct{}) // do not retry on every pass
			}
		}
		rows := currentRows(p, collapsed)
		row, ok, err := choose(rows, collapsed, cursor, self, stale)
		if err != nil {
			return err
		}
		if !ok {
			cursor = 0
			continue
		}
		if row.state == "" { // a directory heading: fold or unfold its group
			collapsed[row.dir] = !collapsed[row.dir]
			cursor = 0
			for i, r := range rows {
				if r.state == "" && r.dir == row.dir {
					cursor = i
				}
			}
			continue
		}
		cursor = 0
		switch row.state {
		case "ADOPTED":
			uiMessage(fmt.Sprintf("%q is still running outside tmux: exit it, then topic up", row.label))
			continue
		case "down":
			uiMessage(fmt.Sprintf("picking up %q...", row.label))
			if out, err := exec.Command(self, "up", row.label).CombinedOutput(); err != nil {
				// Attaching to a session that never started would leave the right pane
				// dead with a bare tmux error; say why instead.
				uiMessage(fmt.Sprintf("could not pick up %q: %s", row.label, lastLine(string(out), err)))
				continue
			}
		}
		out, err := uiTmux("show", "-gv", "@right-pane").Output()
		if err != nil {
			return fmt.Errorf("ui: the layout has no right pane: %w", err)
		}
		right := strings.TrimSpace(string(out))
		// =name is an exact match: a plain -t would take a prefix of another topic.
		target := tmuxName(tag, row.label)
		if row.state == "SESSION" { // not a topic: its tmux name is its label
			target = row.label
		}
		attach := "env -u TMUX tmux attach -t " + shellQuote("="+target)
		_ = uiTmux("respawn-pane", "-k", "-t", right, attach).Run()
		_ = uiTmux("select-pane", "-t", right).Run()
	}
}

// choose shows the rows and returns the one picked. fzf gives filtering and live
// reload; without it a numbered menu does the job.
// stale closing means the binary changed: fzf is interrupted so the caller can reload.
func choose(rows []uiRow, collapsed map[string]bool, cursor int, self string, stale <-chan struct{}) (uiRow, bool, error) {
	if fzf, err := exec.LookPath("fzf"); err == nil {
		return chooseFzf(fzf, rows, collapsed, cursor, self, stale)
	}
	return chooseMenu(rows)
}

func chooseFzf(fzf string, rows []uiRow, collapsed map[string]bool, cursor int, self string, stale <-chan struct{}) (uiRow, bool, error) {
	var in strings.Builder
	for _, r := range rows {
		in.WriteString(r.line() + "\n")
	}
	// ctrl-r reloads in a child process, so the folded groups travel on its command line.
	reload := shellQuote(self) + " ui --rows"
	var dirs []string
	for d, folded := range collapsed {
		if folded {
			dirs = append(dirs, d)
		}
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		reload += " " + shellQuote("--collapse="+d)
	}
	// reverse-list puts the prompt at the bottom with the list still running top-down.
	// ctrl-q closes this whole UI instance: its server goes, and the picker with it.
	quit := "TOPIC_UI_SOCKET=" + shellQuote(uiSocket()) + " " + shellQuote(self) + " ui --quit"
	cmd := exec.Command(fzf, "--delimiter=\t", "--with-nth=2", "--ansi", "--no-sort", "--layout=reverse-list",
		"--header=enter: open/fold  ^r: refresh  ^q: quit",
		"--bind=ctrl-q:execute-silent("+quit+")",
		"--bind=ctrl-r:reload("+reload+")",
		"--bind=double-click:accept",
		// Land on the row we left; unbound at once so a later reload keeps its own cursor.
		fmt.Sprintf("--bind=load:pos(%d)+unbind(load)", cursor+1))
	cmd.Stdin = strings.NewReader(in.String())
	cmd.Stderr = os.Stderr // fzf draws on /dev/tty; its stderr carries only errors
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Start()
	if err == nil {
		// An interrupt makes fzf restore the terminal and exit 130, which reads as "nothing
		// chosen"; the caller's loop then notices the new binary.
		done := make(chan struct{})
		go func() {
			select {
			case <-stale:
				_ = cmd.Process.Signal(os.Interrupt)
			case <-done:
			}
		}()
		err = cmd.Wait()
		close(done)
	}
	out := stdout.Bytes()
	if err != nil {
		// Exit 1 is no match, 130 is Esc or ctrl-c: both just mean "nothing chosen". Anything
		// else (2: fzf could not run) would spin this loop forever, so surface it.
		if ee, isExit := err.(*exec.ExitError); isExit && ee.ExitCode() != 2 {
			return uiRow{}, false, nil
		}
		return uiRow{}, false, fmt.Errorf("ui: fzf: %w", err)
	}
	f := strings.Split(strings.TrimRight(string(out), "\n"), "\t")
	if len(f) != 3 {
		return uiRow{}, false, nil
	}
	if f[0] == "" { // a heading hands back its directory
		return uiRow{dir: f[2]}, true, nil
	}
	return uiRow{state: f[0], label: f[2]}, true, nil
}

func chooseMenu(rows []uiRow) (uiRow, bool, error) {
	fmt.Print("\033[H\033[2J")
	// Headings are numbered too: picking one folds or unfolds its group, as in fzf.
	for i, r := range rows {
		if r.state == "" {
			fmt.Printf("%3d  %s\n", i+1, colour(ansiDim, r.headingText()))
			continue
		}
		fmt.Printf("%3d    %s  %s\n", i+1, r.mark(), r.label)
	}
	fmt.Print("\nnumber to open (a directory folds), q to quit, Enter to refresh: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil { // the terminal closed; looping would spin
		return uiRow{}, false, err
	}
	if strings.EqualFold(strings.TrimSpace(line), "q") {
		// This picker dies with the server, so the leftover socket file is swept by the
		// next --list or launch (uiInstances).
		_ = uiTmux("kill-server").Run()
		return uiRow{}, false, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || n < 1 || n > len(rows) {
		return uiRow{}, false, nil
	}
	return rows[n-1], true, nil
}
