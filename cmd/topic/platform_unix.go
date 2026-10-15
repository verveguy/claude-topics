//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Platform seams. Everything that differs between macOS/Linux and Windows lives behind
// these few functions, so the orchestration above them reads the same on both.

const exeSuffix = ""

// alive reports whether a process exists. Signal 0 checks without delivering anything.
func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// startDetached starts cmd in its own session, so killing the caller's tmux pane cannot
// take it down too.
func startDetached(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

// lockFile takes an exclusive advisory lock on f, blocking until it is free.
func lockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX) }

func unlockFile(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }

// childPids lists pid's direct children. A pane's shell starts its program directly,
// so one level is enough here.
func childPids(pid int) []int {
	out, _ := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output()
	var kids []int
	for _, f := range strings.Fields(string(out)) {
		if k := atoi(f); k > 0 {
			kids = append(kids, k)
		}
	}
	return kids
}

// userShell is the shell daemons run under: the user's own, so PATH and rc files match
// what an interactive start would have had.
func userShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/zsh"
}

func tmuxCmd(args ...string) *exec.Cmd { return exec.Command("tmux", args...) }

// tmuxStartSession runs a new-session. tmux daemonises its own server here, so nothing
// more is needed; Windows has to detach it by hand.
func tmuxStartSession(args ...string) error { return tmuxCmd(args...).Run() }

// tmuxShell is the command a new tmux session runs; nil means tmux's default shell.
func tmuxShell() []string { return nil }

// tmuxDir renders a working directory the way tmux expects it.
func tmuxDir(dir string) string { return dir }

// Dispatcher agents are launchd plists.
const agentKind = "launchd agents"

// agentFiles lists installed agents. Both labels: io.github.* is current, com.verveguy.*
// is what installs made before the namespace was anchored to a domain that exists.
// Missing an agent here would mean `cycle down` leaves it supervising, which is the
// exact failure that command exists to prevent.
func agentFiles(home string) []string {
	var paths []string
	for _, pat := range []string{"io.github.verveguy.claude-dispatcher*.plist", "com.verveguy.claude-dispatcher*.plist"} {
		found, _ := filepath.Glob(filepath.Join(home, "Library", "LaunchAgents", pat))
		paths = append(paths, found...)
	}
	return paths
}

func agentConfigDir(path string) string { return plistConfigDir(path) }

func agentDisableCmd(path string) []string { return []string{"launchctl", "unload", path} }

func agentEnableCmd(path string) []string { return []string{"launchctl", "load", "-w", path} }
