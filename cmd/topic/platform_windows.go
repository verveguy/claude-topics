//go:build windows

package main

// Windows uses MSYS2's tmux (C:\msys64\usr\bin\tmux.exe). It hosts native console
// programs such as claude.exe and capture-pane reads their screen, but only a pane
// running a POSIX shell reports the native child in #{pane_current_command}. With
// tmux's Windows default (cmd.exe) the pane reads "cmd.exe" whether Claude is running
// or not, so isUp could not tell a live topic from a husk. Hence tmuxShell.

import (
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"unsafe"
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

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
)

const lockfileExclusiveLock = 0x2

// lockFile takes an exclusive lock on f's first byte, blocking until it is free. There
// is no flock on Windows; LockFileEx is the equivalent, and is released when the handle
// closes even if unlockFile is never reached.
func lockFile(f *os.File) error {
	var ol syscall.Overlapped
	r, _, err := procLockFileEx.Call(f.Fd(), lockfileExclusiveLock, 0, 1, 0, uintptr(unsafe.Pointer(&ol)))
	if r == 0 {
		return err
	}
	return nil
}

func unlockFile(f *os.File) {
	var ol syscall.Overlapped
	_, _, _ = procUnlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&ol)))
}

// childPids lists the WINDOWS pids running under a pane whose tmux-reported pid is pid.
//
// Two things differ from Unix. tmux here is an MSYS2 program, so #{pane_pid} is an MSYS
// pid, while Claude Code keys its session records by Windows pid: translate through
// /proc/<pid>/winpid first. And MSYS starts a native program from a forked stub of the
// shell, so the program is a grandchild, not a child: return every descendant.
func childPids(pid int) []int {
	root := msysWinPid(pid)
	if root == 0 {
		root = pid // not an MSYS pid after all: take it as a Windows one
	}
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer syscall.CloseHandle(snap)
	children := map[uint32][]uint32{}
	var e syscall.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = syscall.Process32First(snap, &e); err == nil; err = syscall.Process32Next(snap, &e) {
		children[e.ParentProcessID] = append(children[e.ParentProcessID], e.ProcessID)
	}
	var out []int
	queue := []uint32{uint32(root)}
	seen := map[uint32]bool{uint32(root): true}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, c := range children[p] {
			if !seen[c] { // pid reuse can make the parent map cyclic
				seen[c] = true
				out = append(out, int(c))
				queue = append(queue, c)
			}
		}
	}
	return out
}

// msysWinPid maps an MSYS2 pid to its Windows pid, or 0 if it cannot. Only an MSYS
// program can read MSYS's /proc, so this asks MSYS's own cat.
func msysWinPid(pid int) int {
	cat := filepath.Join(filepath.Dir(tmuxBin()), "cat.exe")
	cmd := msysCmd(cat, fmt.Sprintf("/proc/%d/winpid", pid))
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	return atoi(strings.TrimSpace(string(out)))
}

// userShell is always MSYS2's bash, as in tmuxShell. $SHELL cannot be trusted here: over
// SSH, OpenSSH sets it to its configured shell (powershell.exe on windoze), and a daemon
// started as `powershell.exe -lic ...` dies at once. Seen 2026-10-15.
func userShell() string { return "/usr/bin/bash" }

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
func tmuxCmd(args ...string) *exec.Cmd { return msysCmd(tmuxBin(), args...) }

// msysCmd runs an MSYS2 program from this NATIVE process, with its command line built by
// hand. The MSYS2 runtime parses a native caller's command line by its own rules, not
// Windows': inside double quotes a backslash escapes the next character, and unquoted
// text is glob- and brace-expanded. Go's quoting follows Windows' rules, so a seed prompt
// quoting a topic name — `the topic "A b"` — reached tmux as `\Ab\`, and a daemon's
// start line split apart. Measured on windoze, 2026-10-15: quoting EVERY argument as
// "..." with \ and " backslash-escaped reaches the program exactly, #{...} included.
//
// That quoting also stops the brace expansion that once turned #{pane_current_command}
// into "#pane_current_command" (every husk then read as a running Claude), which is
// what MSYS=noglob was for. noglob must NOT be set on these calls: it also turns off
// the backslash unescaping, mangling every quote. Panes still get it — see
// tmuxStartSession — because claude.exe's own tmux calls are quoted the Windows way.
func msysCmd(prog string, args ...string) *exec.Cmd {
	cmd := exec.Command(prog, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: msysCmdLine(prog, args)}
	var msys []string
	for _, f := range strings.Fields(os.Getenv("MSYS")) {
		if f != "noglob" {
			msys = append(msys, f)
		}
	}
	cmd.Env = append(os.Environ(), "MSYSTEM=MSYS", "MSYS2_PATH_TYPE=inherit", "CHERE_INVOKING=1",
		"MSYS="+strings.Join(msys, " "))
	return cmd
}

var msysEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

func msysCmdLine(prog string, args []string) string {
	q := make([]string, 0, len(args)+1)
	for _, a := range append([]string{prog}, args...) {
		q = append(q, `"`+msysEscaper.Replace(a)+`"`)
	}
	return strings.Join(q, " ")
}

// paneEnv is set in every pane topic creates. claude.exe asks tmux for its session name
// with an unquoted #{...} format; without noglob, MSYS brace-expands it.
var paneEnv = []string{"-e", "MSYS=noglob"}

// tmuxStartSession runs a new-session, which may start the tmux server, so the server
// must not belong to whatever launched topic. Under Task Scheduler the Dispatcher task
// otherwise never finishes (skipping every later check, since overlapping runs are
// ignored) and its time limit kills the server and every topic with it. Breaking away
// from the job and detaching from the console fixes both; as in startDetached, a job
// that forbids breakaway gets the detached start alone.
func tmuxStartSession(args ...string) error {
	if len(args) > 0 && args[0] == "new-session" {
		args = append(append([]string{args[0]}, paneEnv...), args[1:]...)
	}
	flags := []uint32{
		detachedProcess | createNewProcessGroup | createBreakawayFromJob,
		detachedProcess | createNewProcessGroup,
	}
	var err error
	for _, f := range flags {
		cmd := tmuxCmd(args...)
		cmd.SysProcAttr.CreationFlags = f // keep the hand-built command line
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

// replaceProcess has no exec on Windows: run path as a child on the same console, wait
// for it, and exit with its status. It returns only if the child could not be started.
func replaceProcess(path string, args []string) error {
	cmd := exec.Command(path, args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := cmd.Wait(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		os.Exit(1)
	}
	os.Exit(0)
	return nil
}
