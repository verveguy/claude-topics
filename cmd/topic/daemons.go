package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Daemons are long-running processes that are not Claude topics but live the way
// topics do: each in its own detached tmux session, inside an interactive shell. The
// Fabrik engines and the Pruefer reviewers are the motivating case.
//
// Nothing used to supervise them. A SIGTERM — a profile being logged out, a stray
// `pkill`, a clean shutdown from their own TUI — left the pane back at a shell prompt,
// and there it stayed. On 2026-10-05 four Fabrik daemons went down within 80 seconds
// of each other and stayed down for hours until someone happened to look.
//
// So the Dispatcher's five-minute launchd cycle (`ensure-dispatcher`) now also ensures
// the profile's daemons, the same way it ensures the Dispatcher itself: a missing
// session is created, and a session whose pane is back at a shell — a husk — gets its
// command typed in again. A daemon stopped on purpose is marked `stopped`, so the
// keeper does not fight the person who stopped it.
//
// The registry is per profile (<config dir>/daemons.json) because the agent that runs
// the check is per profile, and a daemon's own `.env` already pins the Claude profile
// its workers use (see `daemon-env`). Which profile's agent supervises a daemon is
// therefore just bookkeeping: put it beside the profile it runs under.

// daemonSpec is one supervised daemon.
type daemonSpec struct {
	// Name is the tmux session name, used verbatim (daemons are not namespaced by
	// profile the way topics are; their names are already unique).
	Name string `json:"name"`
	// Dir is the session's working directory.
	Dir string `json:"dir"`
	// Command is typed into the session's shell to start the daemon, e.g.
	// `daemon-env fabrik --auto-upgrade`.
	Command string `json:"command"`
	// Stopped marks a daemon that was stopped on purpose; the keeper leaves it down.
	Stopped bool `json:"stopped,omitempty"`
}

type daemonFile struct {
	Daemons []daemonSpec `json:"daemons"`
}

func daemonsPath(configDir string) string { return filepath.Join(configDir, "daemons.json") }

// loadDaemons reads the registry. A missing file is an empty registry, not an error:
// most profiles have no daemons.
func loadDaemons(configDir string) ([]daemonSpec, error) {
	b, err := os.ReadFile(daemonsPath(configDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f daemonFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", daemonsPath(configDir), err)
	}
	return f.Daemons, nil
}

// saveDaemons writes the registry atomically, sorted by name for stable diffs.
func saveDaemons(configDir string, ds []daemonSpec) error {
	sort.Slice(ds, func(i, j int) bool { return ds[i].Name < ds[j].Name })
	b, err := json.MarshalIndent(daemonFile{Daemons: ds}, "", "  ")
	if err != nil {
		return err
	}
	p := daemonsPath(configDir)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// daemon states, as the keeper sees them.
const (
	daemonRunning = "running" // something other than a shell is in the foreground
	daemonHusk    = "husk"    // the session exists but its pane is back at a shell
	daemonMissing = "missing" // no tmux session at all
)

// daemonState inspects tmux. It reuses paneRunningClaude, which despite its name asks
// the general question this needs: is anything other than a shell in the foreground?
//
// A shell in the foreground is not enough to call it a husk, though. A daemon started
// through a wrapper script that does not `exec` its program shows the wrapper's shell
// (`bash`, `sh`) as the foreground command while the daemon runs underneath it. Typing
// the start line into that would feed the live daemon stray input, or start a second
// copy. So a pane is a husk only when its shell is in the foreground AND has no child
// processes: an idle prompt. (`daemon-env` does exec, so registered Fabrik and Pruefer
// commands show their own names; this guards the wrappers that don't.)
func daemonState(name string) string {
	switch {
	case !sessionExists(name):
		return daemonMissing
	case paneRunningClaude(name):
		return daemonRunning
	case paneShellHasChildren(name):
		return daemonRunning
	default:
		return daemonHusk
	}
}

// paneShellHasChildren reports whether any pane's shell process has a child: something is
// running under it even though the shell is what tmux reports in the foreground.
func paneShellHasChildren(name string) bool {
	out, err := exec.Command("tmux", "list-panes", "-t", "="+name, "-F", "#{pane_pid}").Output()
	if err != nil {
		return false
	}
	for _, f := range strings.Fields(string(out)) {
		if p := atoi(f); p > 0 && len(childPids(p)) > 0 {
			return true
		}
	}
	return false
}

// daemonAction is what the keeper should do for a daemon in a given state. Pure, so
// the decision table is testable without tmux.
func daemonAction(d daemonSpec, state string) string {
	switch {
	case d.Stopped:
		return "skip"
	case state == daemonMissing:
		return "create-and-start"
	case state == daemonHusk:
		return "start"
	default:
		return "none"
	}
}

// startLine is what gets typed into the shell. The trailing echo makes an exit visible
// in the pane, because a daemon that exits without logging anything (as four did on
// 2026-10-05) otherwise leaves nothing to explain why.
func startLine(d daemonSpec) string {
	return fmt.Sprintf(`%s; echo "[topic] daemon %s exited with status $?"`, d.Command, d.Name)
}

// userShell is the shell daemons run under: the user's own, so PATH and rc files match
// what an interactive start would have had.
func userShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/zsh"
}

// startDaemon starts a daemon in its session.
//
// A new session is created with the command as its program — a login+interactive shell
// that runs it and then becomes a plain shell — rather than created empty and typed
// into. Typing into a brand-new session races the shell's rc files: the keys can land
// before the prompt and run seconds late, after the keeper has already judged the pane.
// An existing husk is a shell that has finished starting, so typing into it is safe.
func startDaemon(d daemonSpec, create bool) error {
	if create {
		sh := userShell()
		prog := fmt.Sprintf("%s -lic %s; exec %s -l", sh, shellQuote(startLine(d)), sh)
		if err := tmuxRun("new-session", "-d", "-s", d.Name, "-c", d.Dir, prog); err != nil {
			return fmt.Errorf("creating tmux session %q: %w", d.Name, err)
		}
		return nil
	}
	// "=name:" targets the session by exact name, then its current window and pane.
	if err := tmuxRun("send-keys", "-t", "="+d.Name+":", startLine(d), "Enter"); err != nil {
		return fmt.Errorf("starting %q: %w", d.Name, err)
	}
	return nil
}

// ensureDaemons is the keeper. It never fails the caller: it runs inside
// ensure-dispatcher, whose job must not be blocked by one bad daemon entry.
func ensureDaemons(configDir string) {
	ds, err := loadDaemons(configDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "topic: daemons: %v\n", err)
		return
	}
	for _, d := range ds {
		state := daemonState(d.Name)
		switch daemonAction(d, state) {
		case "create-and-start":
			fmt.Printf("daemon %q is missing — creating its session and starting it.\n", d.Name)
			if err := startDaemon(d, true); err != nil {
				fmt.Fprintf(os.Stderr, "topic: %v\n", err)
			}
		case "start":
			fmt.Printf("daemon %q is down (its pane is at a shell) — starting it.\n", d.Name)
			if err := startDaemon(d, false); err != nil {
				fmt.Fprintf(os.Stderr, "topic: %v\n", err)
			}
		}
	}
}

// cmdDaemons lists the profile's daemons and their state.
//
//	daemons <config-dir>
func cmdDaemons(args []string) error {
	need(args, 1, "daemons")
	ds, err := loadDaemons(args[0])
	if err != nil {
		return err
	}
	if len(ds) == 0 {
		fmt.Printf("No daemons registered in %s.\n", daemonsPath(args[0]))
		return nil
	}
	for _, d := range ds {
		state := daemonState(d.Name)
		if d.Stopped {
			state += " (stopped on purpose)"
		}
		fmt.Printf("  %-28s %-28s %s\n", d.Name, state, d.Dir)
	}
	return nil
}

// cmdDaemon manages registry entries.
//
//	daemon add <name> <dir> <command...> <config-dir>
//	daemon remove|stop|start <name> <config-dir>
func cmdDaemon(args []string) error {
	need(args, 3, "daemon add|remove|stop|start <name> ...")
	verb, name := args[0], args[1]
	configDir := args[len(args)-1]
	ds, err := loadDaemons(configDir)
	if err != nil {
		return err
	}
	idx := -1
	for i, d := range ds {
		if d.Name == name {
			idx = i
		}
	}
	switch verb {
	case "add":
		need(args, 5, `daemon add "<name>" <dir> <command...>`)
		dir, cmd := args[2], strings.Join(args[3:len(args)-1], " ")
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		if !dirExists(dir) {
			return fmt.Errorf("%s is not a directory", dir)
		}
		d := daemonSpec{Name: name, Dir: dir, Command: cmd}
		if idx >= 0 {
			ds[idx] = d
		} else {
			ds = append(ds, d)
		}
		fmt.Printf("Registered daemon %q: %s (in %s).\n", name, cmd, dir)
		return saveDaemons(configDir, ds)
	case "remove":
		if idx < 0 {
			return fmt.Errorf("no daemon named %q", name)
		}
		ds = append(ds[:idx], ds[idx+1:]...)
		fmt.Printf("Removed daemon %q from the registry (its session, if any, is untouched).\n", name)
		return saveDaemons(configDir, ds)
	case "stop":
		if idx < 0 {
			return fmt.Errorf("no daemon named %q", name)
		}
		ds[idx].Stopped = true
		if err := saveDaemons(configDir, ds); err != nil {
			return err
		}
		if daemonState(name) == daemonRunning {
			// One C-c: Fabrik and Pruefer both treat it as a clean shutdown.
			_ = tmuxRun("send-keys", "-t", "="+name+":", "C-c")
		}
		fmt.Printf("Stopped daemon %q; the keeper will leave it down until `topic daemon start`.\n", name)
		return nil
	case "start":
		if idx < 0 {
			return fmt.Errorf("no daemon named %q", name)
		}
		ds[idx].Stopped = false
		if err := saveDaemons(configDir, ds); err != nil {
			return err
		}
		switch state := daemonState(name); daemonAction(ds[idx], state) {
		case "create-and-start":
			return startDaemon(ds[idx], true)
		case "start":
			return startDaemon(ds[idx], false)
		default:
			fmt.Printf("Daemon %q is already running.\n", name)
		}
		return nil
	default:
		return fmt.Errorf("unknown daemon verb %q (add, remove, stop, start)", verb)
	}
}
