#!/usr/bin/env bash
# install.sh — install this repo into the places Claude Code and launchd look.
#
# The plugin is a SYMLINK back into the repo, so editing a skill takes effect
# immediately. The `topic` binary is a signed COPY in ~/.local/bin, not a symlink:
# launchd runs it at login and it becomes the responsible process for every tmux
# session, so it must live on the boot volume (a repo on an external drive is behind
# macOS's Removable Volumes gate) and be the file any Full Disk Access grant names.
# Re-run after changing anything under cmd/.
#
#   ./install.sh            install into the active profile (CLAUDE_CONFIG_DIR)
#   ./install.sh --all      reinstall every profile that already has an agent
#   ./install.sh --default  allow installing the default profile (see below)
#   ./install.sh --force    replace existing files, backing them up to *.bak
#   ./install.sh --uninstall
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="$HOME/.local/bin"

# Install into whichever Claude Code profile is active, exactly as `claude` and
# `topic` resolve it. A second profile can be installed alongside the first:
#   CLAUDE_CONFIG_DIR=~/.claude-work ./install.sh
CLAUDE_DIR="${CLAUDE_CONFIG_DIR:-$HOME/.claude}"
PLUGIN_DEST="$CLAUDE_DIR/skills/topics"

# The launchd label and log must differ per profile, or installing the second
# profile would silently replace the first profile's Dispatcher agent. The default
# profile keeps the original label so existing installs are not orphaned.
PROFILE_SUFFIX=""
if [[ "$CLAUDE_DIR" != "$HOME/.claude" ]]; then
  PROFILE_SUFFIX="-$(basename "$CLAUDE_DIR" | sed 's/^\.//; s/^claude-//')"
fi
PLIST_LABEL="io.github.verveguy.claude-dispatcher$PROFILE_SUFFIX"
PLIST_DEST="$HOME/Library/LaunchAgents/$PLIST_LABEL.plist"
# Agents installed before the label was anchored to a domain that actually exists.
LEGACY_PLIST="$HOME/Library/LaunchAgents/com.verveguy.claude-dispatcher$PROFILE_SUFFIX.plist"
LOG_PATH="$HOME/Library/Logs/claude-dispatcher$PROFILE_SUFFIX.log"

FORCE=0; UNINSTALL=0; ALL=0; DEFAULT_OK=0
for a in "$@"; do
  case "$a" in
    --force) FORCE=1 ;;
    --uninstall) UNINSTALL=1 ;;
    --all) ALL=1 ;;
    --default) DEFAULT_OK=1 ;;
    *) echo "unknown option: $a" >&2; exit 2 ;;
  esac
done

say() { printf '  %s\n' "$*"; }

AGENTS_DIR="$HOME/Library/LaunchAgents"

# --all: re-run this script once per installed Dispatcher agent, with the profile
# that agent was rendered for. The agent's own EnvironmentVariables are the record
# of which profile it serves; no CLAUDE_CONFIG_DIR there means the default profile.
if [[ $ALL -eq 1 ]]; then
  [[ $UNINSTALL -eq 1 ]] && { echo "--all and --uninstall cannot be combined" >&2; exit 2; }
  pass=(); [[ $FORCE -eq 1 ]] && pass+=(--force)
  shopt -s nullglob
  plists=("$AGENTS_DIR"/io.github.verveguy.claude-dispatcher*.plist)
  [[ ${#plists[@]} -gt 0 ]] || { echo "--all: no Dispatcher agents installed; run ./install.sh first" >&2; exit 1; }
  for p in "${plists[@]}"; do
    dir="$(/usr/libexec/PlistBuddy -c 'Print :EnvironmentVariables:CLAUDE_CONFIG_DIR' "$p" 2>/dev/null || true)"
    echo "== $(basename "$p" .plist)"
    if [[ -n "$dir" ]]; then
      CLAUDE_CONFIG_DIR="$dir" bash "$REPO/install.sh" "${pass[@]+"${pass[@]}"}"
    else
      env -u CLAUDE_CONFIG_DIR bash "$REPO/install.sh" --default "${pass[@]+"${pass[@]}"}"
    fi
  done
  exit 0
fi

# Guard the default profile. Forgetting CLAUDE_CONFIG_DIR is the easy mistake on a
# multi-profile machine, and it would quietly add a Dispatcher agent (and a
# Dispatcher) for a profile you do not use. So with CLAUDE_CONFIG_DIR unset, go ahead
# only when that is plainly meant: the default profile is already installed, nothing
# is installed yet (a first install), or --default says so.
if [[ "$CLAUDE_DIR" == "$HOME/.claude" && $DEFAULT_OK -eq 0 ]]; then
  shopt -s nullglob
  others=("$AGENTS_DIR"/io.github.verveguy.claude-dispatcher-*.plist)
  shopt -u nullglob
  if [[ ! -f "$PLIST_DEST" && ! -f "$LEGACY_PLIST" && ${#others[@]} -gt 0 ]]; then
    echo "install.sh: CLAUDE_CONFIG_DIR is unset, so this would target the default profile" >&2
    echo "  (~/.claude), which has no Dispatcher agent here. Installed profiles:" >&2
    for p in "${others[@]}"; do
      echo "    $(/usr/libexec/PlistBuddy -c 'Print :EnvironmentVariables:CLAUDE_CONFIG_DIR' "$p" 2>/dev/null || basename "$p")" >&2
    done
    echo "  Pick one:  CLAUDE_CONFIG_DIR=~/.claude-<profile> ./install.sh" >&2
    echo "  Or all:    ./install.sh --all" >&2
    echo "  Or, if you really mean the default profile:  ./install.sh --default" >&2
    exit 1
  fi
fi

TOPIC_ID="${TOPIC_IDENTIFIER:-io.github.verveguy.topic}"

# is_our_binary <path>: a regular file signed with topic's identifier.
is_our_binary() {
  [[ -f "$1" && ! -L "$1" ]] || return 1
  [[ "$(codesign -d --verbose=2 "$1" 2>&1 | sed -n 's/^Identifier=//p')" == "$TOPIC_ID" ]]
}

# install_bin <src> <dest>: copy a signed binary into place.
#
# Atomic on purpose: the copy lands beside <dest> and is renamed over it. Writing
# into the existing file instead would modify pages of a running, code-signed
# binary (the Dispatcher agents run it every 5 minutes) and macOS kills the
# process with "Code Signature Invalid".
install_bin() {
  local src="$1" dest="$2"
  if [[ -e "$dest" || -L "$dest" ]]; then
    if [[ ! -L "$dest" ]] && cmp -s "$src" "$dest"; then
      say "ok (up to date): $dest"; return 0
    fi
    if [[ $FORCE -eq 0 && ! -L "$dest" ]] && ! is_our_binary "$dest"; then
      echo "refusing to overwrite unrecognised file: $dest (use --force)" >&2; return 1
    fi
    if [[ $FORCE -eq 1 && ! -L "$dest" ]] && ! is_our_binary "$dest"; then
      cp -p "$dest" "$dest.bak"; say "backed up -> $dest.bak"
    fi
  fi
  mkdir -p "$(dirname "$dest")"
  local tmp="$dest.new.$$"
  cp -p "$src" "$tmp"
  mv -f "$tmp" "$dest"
  say "installed $dest (copy of $src)"
}

if [[ $UNINSTALL -eq 1 ]]; then
  echo "Uninstalling…"
  for p in "$PLIST_DEST" "$LEGACY_PLIST"; do
    [[ -f "$p" ]] || continue
    launchctl unload "$p" 2>/dev/null || true
    rm -f "$p"; say "removed $p"
  done
  # The binary is shared by every profile: keep it while another profile's agent uses it.
  others=$(ls "$HOME"/Library/LaunchAgents/{io.github,com}.verveguy.claude-dispatcher*.plist 2>/dev/null | wc -l | tr -d ' ')
  if [[ "$others" -gt 0 ]]; then
    say "kept $BIN/topic: $others other profile agent(s) still use it"
  elif [[ -L "$BIN/topic" ]] || is_our_binary "$BIN/topic"; then
    rm -f "$BIN/topic"; say "removed $BIN/topic"
  fi
  [[ -L "$BIN/topics-cycle" ]] && { rm -f "$BIN/topics-cycle"; say "removed $BIN/topics-cycle"; }
  [[ -L "$PLUGIN_DEST" ]] && { rm -f "$PLUGIN_DEST"; say "removed $PLUGIN_DEST"; }
  echo
  echo "Left in place (deliberately — this is your data, not the tool):"
  say "$CLAUDE_DIR/topics/   topic registry, handoff docs, and briefs"
  exit 0
fi

# link <src> <dest>
link() {
  local src="$1" dest="$2"
  if [[ -e "$dest" || -L "$dest" ]]; then
    if [[ -L "$dest" && "$(readlink "$dest")" == "$src" ]]; then
      say "ok (already linked): $dest"; return 0
    fi
    if [[ $FORCE -eq 0 && ! -L "$dest" ]]; then
      echo "refusing to overwrite real file: $dest (use --force)" >&2; return 1
    fi
    [[ ! -L "$dest" ]] && { mv "$dest" "$dest.bak"; say "backed up -> $dest.bak"; }
    rm -rf "$dest"
  fi
  mkdir -p "$(dirname "$dest")"
  ln -s "$src" "$dest"
  say "linked $dest -> $src"
}

# Build first: everything below installs or uses bin/topic.
#
# scripts/build.sh is the only supported build path: as well as compiling, it gives
# the binary a stable code-signing identity, without which macOS re-asks for
# permissions forever and blames `topic` for requests its children made. The script
# explains the mechanism in full; README has the short version.
"$REPO/scripts/build.sh" || exit 1

# topics-cycle became `topic cycle`; clear the stale symlink so it cannot shadow it.
[[ -L "$BIN/topics-cycle" ]] && { rm -f "$BIN/topics-cycle"; say "removed the old $BIN/topics-cycle (now: topic cycle)"; }

echo "Installing from $REPO"
say "profile: $CLAUDE_DIR"
mkdir -p "$BIN"
install_bin "$REPO/bin/topic"   "$BIN/topic"

link "$REPO/plugin"    "$PLUGIN_DEST"

# The plist cannot be a symlink to a template — launchd needs the real paths
# baked in — so render it.
sed -e "s|__HOME__|$HOME|g" \
    -e "s|__LABEL__|$PLIST_LABEL|g" \
    -e "s|__CONFIG_DIR__|$CLAUDE_DIR|g" \
    -e "s|__LOG__|$LOG_PATH|g" \
  "$REPO/launchd/io.github.verveguy.claude-dispatcher.plist.template" > "$PLIST_DEST"

# The default profile must run with CLAUDE_CONFIG_DIR *unset*, not set to ~/.claude:
# the two select different config files and only the unset one has completed
# onboarding. Setting it lands the Dispatcher on the first-run theme picker.
if [[ "$CLAUDE_DIR" == "$HOME/.claude" ]]; then
  python3 -c '
import plistlib, sys
f = sys.argv[1]
d = plistlib.load(open(f, "rb"))
env = d.get("EnvironmentVariables") or {}
env.pop("CLAUDE_CONFIG_DIR", None)
d["EnvironmentVariables"] = env
plistlib.dump(d, open(f, "wb"))' "$PLIST_DEST"
  say "default profile: CLAUDE_CONFIG_DIR deliberately left unset in the agent"
fi
say "rendered $PLIST_DEST"

# Retire the old-label agent BEFORE loading the new one, or two agents would both be
# supervising the same Dispatcher.
if [[ -f "$LEGACY_PLIST" ]]; then
  launchctl unload "$LEGACY_PLIST" 2>/dev/null || true
  rm -f "$LEGACY_PLIST"
  say "retired the old agent: $(basename "$LEGACY_PLIST")"
fi

launchctl unload "$PLIST_DEST" 2>/dev/null || true
launchctl load -w "$PLIST_DEST"
say "launchd agent loaded (Dispatcher will start within ~5 min, or now)"

echo
echo "Done. Check with:"
say "topic --help"
say "topic list"
say "claude plugin details topics"
echo
case ":$PATH:" in
  *":$BIN:"*) ;;
  *) echo "NOTE: $BIN is not on your PATH — add it to your shell profile." ;;
esac
