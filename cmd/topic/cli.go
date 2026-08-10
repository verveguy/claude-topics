package main

// topic — pick up / put down / fork / hand off long-running Claude Code "topics".
//
// The front door. Everything it needs about the environment is derived here, once, so
// the command implementations are pure functions of their arguments and can be driven
// directly in tests.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const usageText = `topic — pick up / put down / hand off long-running Claude Code "topics".

A TOPIC is a stable name ("Fantasy Economics") that outlives any single Claude
session. The session underneath gets recycled; the topic name does not. That is
what makes hand-off possible.

  topic up   "<name>" [dir]   Pick up: resume the topic's session, or start one.
                              --seed-from <file> starts a NEW topic from a brief.
  topic down "<name>"         Put down: terminate the session, stay resumable.
  topic down-all              Put every live topic in this profile down. Skips the
                              calling session unless --include-self. --dry-run.
  topic list [--all]          Show every topic and whether it is up or down.
                              --all crosses every profile on the machine.
  topic status "<name>"       Detail for one topic.
  topic forget "<name>"       Stop tracking a topic (keeps handoff docs; --purge
                              removes them). The session itself is untouched.
  topic prune                 Show topics that are truly dead — down, with no
                              transcript left to resume. --yes removes them.
  topic adopt "<name>"        Track a session topic did not launch — live (via
                              --dry-run shows what it would adopt, changing nothing.
                              ~/.claude/sessions/<pid>.json) or merely resumable
                              (via its transcript title). --pid / --dir / --title
                              disambiguate; --probe <token> handles a running but
                              unnamed session.

  topic handoff-path "<name>" Print where the handoff doc must be written.
  topic handoff-swap "<name>" Retire the old session, start a fresh one seeded
                              with the handoff doc, same topic name.

  topic fork-path "<new>"     Print where the fork brief must be written.
  topic fork "<new>" [dir]    Split a thread into its own topic, seeded with that
                              brief. --from "<parent>" defaults to the calling
                              topic; the directory defaults to the parent's.
  topic whoami                Name the topic the caller is sitting in.

  topic profiles              Every Claude Code profile, and its topic count.
  topic move "<name>" --to <profile>
                              (--carry-approvals copies your trust / external-import
                              answers for its directory into the destination.)
                              Move a topic — registry, docs, and transcripts — into
                              another profile. A live topic is put down and brought
                              back up on the far side (--stay-down to leave it down).
                              --copy leaves the original, --dry-run shows the plan.

  topic rename "<old>" "<new>"
                              Rename a topic everywhere: registry, tmux, peer name,
                              Claude UI name, and transcript title.
  topic rebridge "<name>"|--all [--down-only]
                              Drop a session's recorded Remote Control bridge so the
                              next launch registers a fresh one — under the current
                              profile's account, named after the topic.

  topic ensure-dispatcher     Start the Dispatcher topic if it is not up.
                              (Run by launchd at login and every 5 minutes.)

Each topic is backed by a stable session UUID, so ` + "`" + `up` + "`" + ` is a lossless --resume.
Every session is started with BOTH --remote-control "<name>" and --name "<name>".
Both are required and they do different jobs:
  --remote-control  makes the session reachable from claude.ai / your phone
  --name            sets the display name other Claude sessions see in
                    ListAgents (without it a local session advertises itself
                    as an auto-derived "<dir>-<hash>" and is unaddressable
                    by topic name — verified 2026-08-08)

Singleton: at most one live session per topic, enforced by an exact-match tmux
check plus an atomic lock. Starting a topic that is already up is a no-op.

PROFILES: honours CLAUDE_CONFIG_DIR, exactly as ` + "`" + `claude` + "`" + ` does. Every path — the
topic registry, session records, transcripts — comes from it, sessions are launched
with it, and non-default profiles get their tmux sessions namespaced so two
profiles cannot fight over one topic name. Set it per shell to manage a profile:
  CLAUDE_CONFIG_DIR=~/.claude-work topic list
`

// profile derives everything that depends on WHICH Claude Code profile is active.
type profile struct {
	configDir  string // CLAUDE_CONFIG_DIR, or ~/.claude
	topicsRoot string // CLAUDE_TOPICS_ROOT, or <configDir>/topics
	dispatcher string // this profile's Dispatcher topic name
}

func activeProfile() profile {
	home, _ := os.UserHomeDir()
	// Honour exactly the variable `claude` reads: topic must look at the same
	// sessions, transcripts and registry as the claude it is driving.
	configDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if configDir == "" {
		configDir = filepath.Join(home, ".claude")
	}
	topicsRoot := os.Getenv("CLAUDE_TOPICS_ROOT")
	if topicsRoot == "" {
		topicsRoot = filepath.Join(configDir, "topics")
	}
	// Each profile runs its own Dispatcher, so their names must differ — otherwise
	// they collide as topic names, as Remote Control names, and in the tmux list.
	dispatcher := os.Getenv("CLAUDE_DISPATCHER_NAME")
	if dispatcher == "" {
		dispatcher = "Dispatcher"
		if tag := profileTag(configDir); tag != "" {
			dispatcher = fmt.Sprintf("Dispatcher (%s)", tag)
		}
	}
	return profile{configDir, topicsRoot, dispatcher}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usageText)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	p := activeProfile()

	// withProfile appends the profile the command should act on. Commands take it as
	// arguments rather than reading the environment, so tests can drive them directly.
	withProfile := func(a []string) []string { return append(a, p.configDir, p.topicsRoot) }

	var err error
	switch cmd {
	case "", "-h", "--help", "help":
		fmt.Print(usageText)
		return

	// ---- topics ----------------------------------------------------------------
	case "up":
		err = cmdUp(withProfile(args))
	case "down":
		err = cmdDown(withProfile(requireName(args, `topic down "<name>"`)))
	case "down-all":
		err = cmdDownAll(withProfile(args))
	case "list", "ls":
		err = cmdList(withProfile(args))
	case "status":
		err = cmdStatusCmd(withProfile(requireName(args, `topic status "<name>"`)))
	case "forget":
		err = cmdForget(withProfile(args))
	case "prune":
		err = cmdPrune(withProfile(args))
	case "adopt":
		err = cmdAdopt(withProfile(args))
	case "move":
		err = cmdMove(withProfile(args))
	case "rename":
		err = cmdRename(withProfile(args))
	case "rebridge":
		err = cmdRebridge(withProfile(args))
	case "whoami":
		err = cmdWhoami([]string{p.configDir, p.topicsRoot})
	case "profiles":
		err = cmdProfiles([]string{p.configDir})

	// ---- documents and the sessions they seed -----------------------------------
	case "handoff-path":
		err = cmdPath(append([]string{"handoffs"}, append(requireName(args, `topic handoff-path "<name>"`), p.topicsRoot)...))
	case "fork-path":
		err = cmdPath(append([]string{"briefs"}, append(requireName(args, `topic fork-path "<new topic>"`), p.topicsRoot)...))
	case "handoff-swap":
		err = cmdHandoffSwap(withProfile(args))
	case "fork":
		err = cmdFork(withProfile(args))

	case "ensure-dispatcher":
		err = cmdEnsureDispatcher([]string{p.dispatcher, p.configDir, p.topicsRoot})

	// ---- low-level helpers, kept for debugging -----------------------------------
	case "get":
		err = cmdGet(args)
	case "set":
		err = cmdSet(args)
	case "sessions":
		err = cmdSessions(args)
	case "find-titled":
		err = cmdFindTitled(args)
	case "transcript-title":
		err = cmdTranscriptTitle(args)
	case "transcript-cwd":
		err = cmdTranscriptCwd(args)

	default:
		fail("unknown command: %s (try: topic --help)", cmd)
	}
	if err != nil {
		fail("%v", err)
	}
}

func requireName(args []string, usage string) []string {
	if len(args) == 0 || strings.HasPrefix(args[0], "--") {
		fail("usage: %s", usage)
	}
	return args
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "topic: "+format+"\n", a...)
	os.Exit(1)
}

func need(args []string, n int, usage string) {
	if len(args) < n {
		fail("usage: topic %s", usage)
	}
}
