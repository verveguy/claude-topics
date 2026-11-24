package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// A daemon's own processes, as opposed to its tmux pane.
//
// `tmux kill-server` does not reliably take a daemon down with its pane. On 2026-11-22
// `topic cycle down && tmux kill-server && topic cycle up` left all twelve registered
// daemons running with no tty, reparented to launchd and still holding their lock
// files. The keeper then saw empty sessions, started fresh copies, and each died at once
// on the lock — and would have every five minutes, forever. Those orphans were also
// invisible: `topic daemons` looks only at panes.
//
// So `cycle down` now stops daemons by signalling their processes and waiting for them
// to exit, and the keeper remembers which processes it last saw running each daemon.
// A remembered process still alive outside its pane is an orphan: the keeper stops it
// before starting the daemon again, and `topic daemons` reports it.

// procEntry identifies a process across time. A pid alone can be reused; a pid with
// its start time cannot.
type procEntry struct {
	PID   int    `json:"pid"`
	Start string `json:"start"`
}

// Platform seams, swappable in tests.
var (
	lookupProc   = procLookup    // (name, start time, ok)
	terminatePID = procTerminate // SIGTERM
	panePIDs     = tmuxPanePIDs
	childPIDs    = childPids
)

func tmuxPanePIDs(session string) []int {
	out, err := tmuxCmd("list-panes", "-t", "="+session+":", "-F", "#{pane_pid}").Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, f := range strings.Fields(string(out)) {
		if p := atoi(f); p > 0 {
			pids = append(pids, p)
		}
	}
	return pids
}

// daemonProcesses finds the processes running a daemon: under each of its panes, the
// first process down each branch that is not a shell. Shells are walked through, since
// a daemon may sit under the pane's shell, a `zsh -lic` wrapper, or a script that does
// not exec. Nil where processes cannot be inspected (Windows).
func daemonProcesses(session string) []procEntry {
	var out []procEntry
	seen := map[int]bool{}
	queue := panePIDs(session)
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		name, start, ok := lookupProc(pid)
		if !ok {
			continue
		}
		if shellCommands[paneCommandName(name)] {
			queue = append(queue, childPIDs(pid)...)
			continue
		}
		out = append(out, procEntry{pid, start})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}

// stillRunning keeps the entries whose process is alive and is the same process.
func stillRunning(ps []procEntry) []procEntry {
	var out []procEntry
	for _, p := range ps {
		if _, start, ok := lookupProc(p.PID); ok && start == p.Start {
			out = append(out, p)
		}
	}
	return out
}

// orphans are remembered processes still alive but no longer in the daemon's pane.
func orphans(remembered, inPane []procEntry) []procEntry {
	here := map[int]bool{}
	for _, p := range inPane {
		here[p.PID] = true
	}
	var out []procEntry
	for _, p := range stillRunning(remembered) {
		if !here[p.PID] {
			out = append(out, p)
		}
	}
	return out
}

func pidList(ps []procEntry) string {
	var s []string
	for _, p := range ps {
		if p.PID > 0 {
			s = append(s, fmt.Sprint(p.PID))
		}
	}
	if len(s) == 0 {
		return "?"
	}
	return strings.Join(s, ", ")
}

// ---- what the keeper remembers ------------------------------------------------------

func daemonPIDsPath(configDir string) string { return filepath.Join(configDir, "daemon-pids.json") }

func loadDaemonPIDs(configDir string) map[string][]procEntry {
	m := map[string][]procEntry{}
	if b, err := os.ReadFile(daemonPIDsPath(configDir)); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

// rememberDaemon records the processes now running a daemon, writing only on change.
func rememberDaemon(configDir, name string, ps []procEntry) {
	if len(ps) == 0 {
		return
	}
	m := loadDaemonPIDs(configDir)
	if old, _ := json.Marshal(m[name]); string(old) == mustJSON(ps) {
		return
	}
	m[name] = ps
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return
	}
	tmp := daemonPIDsPath(configDir) + ".tmp"
	if os.WriteFile(tmp, append(b, '\n'), 0o644) == nil {
		_ = os.Rename(tmp, daemonPIDsPath(configDir))
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// ---- stopping -----------------------------------------------------------------------

// daemonStopWait is how long a stopped daemon gets to exit. Fabrik and Pruefer finish
// their shutdown within about 25 seconds.
var daemonStopWait = 60 * time.Second

// stopResult is how one daemon's stop went.
type stopResult struct {
	name       string
	stopped    []procEntry
	stragglers []procEntry
	took       time.Duration
}

// stopProcesses SIGTERMs processes and waits for them all to exit.
func stopProcesses(ps []procEntry, wait time.Duration) (left []procEntry, took time.Duration) {
	begin := time.Now()
	for _, p := range ps {
		_ = terminatePID(p.PID)
	}
	deadline := begin.Add(wait)
	for {
		left = stillRunning(ps)
		if len(left) == 0 || time.Now().After(deadline) {
			return left, time.Since(begin)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// stopDaemonsForCycle stops every running daemon of the given profiles, in parallel,
// without marking them stopped: `cycle up` is meant to start them again. A daemon whose
// processes cannot be inspected (Windows) gets the C-c `daemon stop` sends instead.
func stopDaemonsForCycle(configDirs []string, dry bool) []stopResult {
	var mu sync.Mutex
	var wg sync.WaitGroup
	var results []stopResult
	for _, dir := range configDirs {
		ds, err := loadDaemons(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "topic: daemons: %v\n", err)
			continue
		}
		for _, d := range ds {
			if daemonState(d.Name) != daemonRunning {
				continue
			}
			ps := daemonProcesses(d.Name)
			if dry {
				fmt.Printf("  would stop %s (pid %s)\n", d.Name, pidList(ps))
				continue
			}
			wg.Add(1)
			go func(d daemonSpec, ps []procEntry) {
				defer wg.Done()
				r := stopResult{name: d.Name, stopped: ps}
				if len(ps) == 0 {
					_ = tmuxRun("send-keys", "-t", "="+d.Name+":", "C-c")
					begin := time.Now()
					for daemonState(d.Name) == daemonRunning && time.Since(begin) < daemonStopWait {
						time.Sleep(250 * time.Millisecond)
					}
					r.took = time.Since(begin)
					if daemonState(d.Name) == daemonRunning {
						r.stragglers = []procEntry{{}}
					}
				} else {
					r.stragglers, r.took = stopProcesses(ps, daemonStopWait)
				}
				mu.Lock()
				results = append(results, r)
				mu.Unlock()
			}(d, ps)
		}
	}
	wg.Wait()
	sort.Slice(results, func(i, j int) bool { return results[i].name < results[j].name })
	return results
}
