//go:build windows

package main

// Windows uses MSYS2's tmux (C:\msys64\usr\bin\tmux.exe). It hosts native console
// programs such as claude.exe and capture-pane reads their screen, but only a pane
// running a POSIX shell reports the native child in #{pane_current_command}. With
// tmux's Windows default (cmd.exe) the pane reads "cmd.exe" whether Claude is running
// or not, so isUp could not tell a live topic from a husk. Hence tmuxShell.

import (
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

const exeSuffix = ".exe"

const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259

	detachedProcess        = 0x00000008
	createNewProcessGroup  = 0x00000200
	createBreakawayFromJob = 0x01000000
)

// alive reports whether a process is still running. Windows has no signal 0, and
// os.FindProcess succeeds for exited pids, so ask for the exit code instead.
func alive(pid int) bool {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}

// startDetached starts cmd with no console and in its own process group, so the dying
// session cannot take it down. It first tries to break away from any job object as
// well: a caller that runs its tools inside a kill-on-close job would otherwise take
// the child with it. Jobs that forbid breakaway reject that, so fall back without it.
func startDetached(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: detachedProcess | createNewProcessGroup | createBreakawayFromJob,
	}
	if err := cmd.Start(); err == nil {
		return nil
	}
	retry := exec.Command(cmd.Path, cmd.Args[1:]...)
	retry.Env, retry.Dir, retry.Stdout, retry.Stderr = cmd.Env, cmd.Dir, cmd.Stdout, cmd.Stderr
	retry.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess | createNewProcessGroup}
	return retry.Start()
}

// tmuxBin finds MSYS2's tmux: TOPIC_TMUX, then PATH, then the default MSYS2 install.
func tmuxBin() string {
	if p := os.Getenv("TOPIC_TMUX"); p != "" {
		return p
	}
	if p, err := exec.LookPath("tmux"); err == nil {
		return p
	}
	return `C:\msys64\usr\bin\tmux.exe`
}

// tmuxCmd runs tmux with the MSYS2 environment its panes need. The first client starts
// the server, and every pane inherits the server's environment, so this is what puts
// the Windows PATH (and with it claude.exe) inside each pane.
//
// MSYS=noglob is load-bearing. The MSYS2 runtime glob- and brace-expands the command
// line of any MSYS program started from a NATIVE process, so from topic.exe
// `-F #{pane_current_command}` reached tmux as "#pane_current_command" — never a shell
// name, so every husk read as a running Claude. Panes inherit it too, which fixes the
// same mangling when claude.exe itself asks tmux for its session name.
func tmuxCmd(args ...string) *exec.Cmd {
	cmd := exec.Command(tmuxBin(), args...)
	msys := "noglob"
	if cur := os.Getenv("MSYS"); cur != "" && !strings.Contains(cur, "noglob") {
		msys = cur + " noglob"
	}
	cmd.Env = append(os.Environ(), "MSYSTEM=MSYS", "MSYS2_PATH_TYPE=inherit", "CHERE_INVOKING=1", "MSYS="+msys)
	return cmd
}

// tmuxStartSession runs a new-session, which may start the tmux server, so the server
// must not belong to whatever launched topic. Under Task Scheduler the Dispatcher task
// otherwise never finishes (skipping every later check, since overlapping runs are
// ignored) and its time limit kills the server and every topic with it. Breaking away
// from the job and detaching from the console fixes both; as in startDetached, a job
// that forbids breakaway gets the detached start alone.
func tmuxStartSession(args ...string) error {
	flags := []uint32{
		detachedProcess | createNewProcessGroup | createBreakawayFromJob,
		detachedProcess | createNewProcessGroup,
	}
	var err error
	for _, f := range flags {
		cmd := tmuxCmd(args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: f}
		if err = cmd.Start(); err != nil {
			continue // could not even start (e.g. breakaway refused): try the next flags
		}
		return cmd.Wait()
	}
	return err
}

// tmuxShell makes each pane a login bash rather than cmd.exe; see the file comment.
func tmuxShell() []string { return []string{"/usr/bin/bash", "-l"} }

// tmuxDir converts C:\Users\me to /c/Users/me, the form MSYS2 tmux resolves reliably.
func tmuxDir(dir string) string {
	if len(dir) >= 2 && dir[1] == ':' {
		return "/" + strings.ToLower(dir[:1]) + filepath.ToSlash(dir[2:])
	}
	return filepath.ToSlash(dir)
}

// Dispatcher agents are Task Scheduler tasks, registered by install.ps1 from
// windows\claude-dispatcher.xml.template under \claude-topics\.
const agentKind = "Task Scheduler Dispatcher tasks"

const agentTaskFolder = `\claude-topics\`

// agentFiles lists the rendered task XML install.ps1 keeps, one per installed profile.
// Those copies play the role the plists do on macOS: each names the profile it manages.
func agentFiles(home string) []string {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		local = filepath.Join(home, "AppData", "Local")
	}
	paths, _ := filepath.Glob(filepath.Join(local, "claude-topics", "tasks", "claude-dispatcher*.xml"))
	return paths
}

// install.ps1 writes `set CLAUDE_CONFIG_DIR=<dir>&& ` unquoted, because conhost
// --headless mangles quoted arguments.
var taskConfigDir = regexp.MustCompile(`set CLAUDE_CONFIG_DIR=([^&]*)&&`)

// agentConfigDir reads the profile out of the task's command line. The default profile's
// task sets nothing, deliberately, so "" is the right answer for it.
func agentConfigDir(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	s := string(b)
	start, end := strings.Index(s, "<Arguments>"), strings.Index(s, "</Arguments>")
	if start < 0 || end < start {
		return ""
	}
	if m := taskConfigDir.FindStringSubmatch(html.UnescapeString(s[start+len("<Arguments>") : end])); m != nil {
		return m[1]
	}
	return ""
}

func agentTaskName(path string) string {
	return agentTaskFolder + strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}

func agentDisableCmd(path string) []string {
	return []string{"schtasks", "/Change", "/TN", agentTaskName(path), "/DISABLE"}
}

func agentEnableCmd(path string) []string {
	return []string{"schtasks", "/Change", "/TN", agentTaskName(path), "/ENABLE"}
}
