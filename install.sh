#!/usr/bin/env bash
# install.sh — link this repo into the places Claude Code and launchd look.
#
# Everything is installed as a SYMLINK back into the repo, so editing a script
# here takes effect immediately with no reinstall step.
#
#   ./install.sh            install (refuses to clobber non-symlink files)
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

FORCE=0; UNINSTALL=0
for a in "$@"; do
  case "$a" in
    --force) FORCE=1 ;;
    --uninstall) UNINSTALL=1 ;;
    *) echo "unknown option: $a" >&2; exit 2 ;;
  esac
done

say() { printf '  %s\n' "$*"; }

if [[ $UNINSTALL -eq 1 ]]; then
  echo "Uninstalling…"
  for p in "$PLIST_DEST" "$LEGACY_PLIST"; do
    [[ -f "$p" ]] || continue
    launchctl unload "$p" 2>/dev/null || true
    rm -f "$p"; say "removed $p"
  done
  [[ -L "$BIN/topic" ]] && { rm -f "$BIN/topic"; say "removed $BIN/topic"; }
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

# The JSON layer is a Go helper; build it before linking anything that needs it.
if command -v go >/dev/null; then
  (cd "$REPO" && go build -o bin/topic ./cmd/topic) \
    && say "built bin/topic"
else
  echo "go not found on PATH — needed to build bin/topic" >&2; exit 1
fi

# topics-cycle became `topic cycle`; clear the stale symlink so it cannot shadow it.
[[ -L "$BIN/topics-cycle" ]] && { rm -f "$BIN/topics-cycle"; say "removed the old $BIN/topics-cycle (now: topic cycle)"; }

echo "Installing from $REPO"
say "profile: $CLAUDE_DIR"
mkdir -p "$BIN"
link "$REPO/bin/topic"          "$BIN/topic"

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
