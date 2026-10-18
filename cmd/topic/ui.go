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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// uiSocket names the tmux server that holds the layout. TOPIC_UI_SOCKET overrides it
// so tests do not collide with a UI the developer has open.
func uiSocket() string {
	if s := os.Getenv("TOPIC_UI_SOCKET"); s != "" {
		return s
	}
	return "topics-ui"
}

// uiTmux runs tmux against the UI's own server.
func uiTmux(args ...string) *exec.Cmd {
	return tmuxCmd(append([]string{"-L", uiSocket()}, args...)...)
}

// uiRow is one line of the picker: a topic, or a directory heading above its topics.
type uiRow struct {
	state string // "UP", "down", "ADOPTED"; empty for a heading
	label string // the topic name, or the directory heading
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
// (in path order), its topics beneath it (in name order). state is injected so the
// grouping can be tested without tmux.
func buildRows(topicsRoot, home string, maxDir int, state func(name string) string) []uiRow {
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
		rows = append(rows, uiRow{label: "-- " + shortDir(d, home, maxDir)})
		names := groups[d]
		sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
		for _, n := range names {
			rows = append(rows, uiRow{state: state(n), label: n})
		}
	}
	return rows
}

func (r uiRow) line() string { return r.state + "\t" + r.label }

func currentRows(p profile) []uiRow {
	home, _ := os.UserHomeDir()
	tag := profileTag(p.configDir)
	return buildRows(p.topicsRoot, home, 28, func(name string) string {
		return topicState(tag, p.topicsRoot, name)
	})
}

// cmdUI dispatches the three modes.
func cmdUI(args []string, p profile) error {
	var rows, pick, noAttach bool
	for _, a := range args {
		switch a {
		case "--rows":
			rows = true
		case "--pick":
			pick = true
		case "--no-attach":
			noAttach = true
		default:
			return fmt.Errorf("ui: unknown option %q (try: topic ui --help)", a)
		}
	}
	switch {
	case rows:
		for _, r := range currentRows(p) {
			fmt.Println(r.line())
		}
		return nil
	case pick:
		return runPicker(p)
	}
	return openUI(p, noAttach)
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
			{"set", "-g", "status-right", " C-a h: list | C-a l: session | C-a d: detach "},
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
		_ = uiTmux("select-pane", "-t", left).Run()
	}
	if noAttach {
		return nil
	}
	cmd := uiTmux("attach", "-t", "="+uiSocket())
	cmd.Env = withoutEnv(os.Environ(), "TMUX") // else tmux refuses to nest
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
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

// runPicker is the left pane: choose a topic, point the right pane at it, repeat.
func runPicker(p profile) error {
	// This pane lives inside the UI's own tmux server, so $TMUX points there; topic's
	// own tmux calls must reach the default server, where the topic sessions are.
	os.Unsetenv("TMUX")
	self, err := os.Executable()
	if err != nil {
		return err
	}
	tag := profileTag(p.configDir)
	for {
		row, ok, err := choose(currentRows(p), self)
		if err != nil {
			return err
		}
		if !ok || row.state == "" { // nothing chosen, or a directory heading
			continue
		}
		switch row.state {
		case "ADOPTED":
			_ = uiTmux("display-message", fmt.Sprintf("%q is still running outside tmux: exit it, then topic up", row.label)).Run()
			continue
		case "down":
			_ = uiTmux("display-message", fmt.Sprintf("picking up %q...", row.label)).Run()
			_ = exec.Command(self, "up", row.label).Run()
		}
		out, err := uiTmux("show", "-gv", "@right-pane").Output()
		if err != nil {
			return fmt.Errorf("ui: the layout has no right pane: %w", err)
		}
		right := strings.TrimSpace(string(out))
		// =name is an exact match: a plain -t would take a prefix of another topic.
		attach := "env -u TMUX tmux attach -t " + shellQuote("="+tmuxName(tag, row.label))
		_ = uiTmux("respawn-pane", "-k", "-t", right, attach).Run()
		_ = uiTmux("select-pane", "-t", right).Run()
	}
}

// choose shows the rows and returns the one picked. fzf gives filtering and live
// reload; without it a numbered menu does the job.
func choose(rows []uiRow, self string) (uiRow, bool, error) {
	if fzf, err := exec.LookPath("fzf"); err == nil {
		return chooseFzf(fzf, rows, self)
	}
	return chooseMenu(rows)
}

func chooseFzf(fzf string, rows []uiRow, self string) (uiRow, bool, error) {
	var in strings.Builder
	for _, r := range rows {
		in.WriteString(r.line() + "\n")
	}
	cmd := exec.Command(fzf, "--delimiter=\t", "--with-nth=1,2", "--no-sort", "--reverse",
		"--header=enter: open  ^r: refresh",
		"--bind=ctrl-r:reload("+shellQuote(self)+" ui --rows)")
	cmd.Stdin = strings.NewReader(in.String())
	cmd.Stderr = os.Stderr // fzf draws on /dev/tty; its stderr carries only errors
	out, err := cmd.Output()
	if err != nil {
		// Exit 1 is no match, 130 is Esc or ctrl-c: both just mean "nothing chosen". Anything
		// else (2: fzf could not run) would spin this loop forever, so surface it.
		if ee, isExit := err.(*exec.ExitError); isExit && ee.ExitCode() != 2 {
			return uiRow{}, false, nil
		}
		return uiRow{}, false, fmt.Errorf("ui: fzf: %w", err)
	}
	state, label, _ := strings.Cut(strings.TrimRight(string(out), "\n"), "\t")
	return uiRow{state: state, label: label}, true, nil
}

func chooseMenu(rows []uiRow) (uiRow, bool, error) {
	fmt.Print("\033[H\033[2J")
	var pickable []uiRow
	for _, r := range rows {
		if r.state == "" {
			fmt.Println(r.label)
			continue
		}
		pickable = append(pickable, r)
		fmt.Printf("%3d  %-7s %s\n", len(pickable), r.state, r.label)
	}
	fmt.Print("\nnumber to open, Enter to refresh: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil { // the terminal closed; looping would spin
		return uiRow{}, false, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || n < 1 || n > len(pickable) {
		return uiRow{}, false, nil
	}
	return pickable[n-1], true, nil
}
