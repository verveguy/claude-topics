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
PLIST_LABEL="com.verveguy.claude-dispatcher$PROFILE_SUFFIX"
PLIST_DEST="$HOME/Library/LaunchAgents/$PLIST_LABEL.plist"
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
  launchctl unload "$PLIST_DEST" 2>/dev/null || true
  rm -f "$PLIST_DEST"; say "removed $PLIST_DEST"
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

echo "Installing from $REPO"
say "profile: $CLAUDE_DIR"
mkdir -p "$BIN"
link "$REPO/bin/topic"        "$BIN/topic"
link "$REPO/bin/topics-cycle" "$BIN/topics-cycle"
link "$REPO/plugin"    "$PLUGIN_DEST"

# The plist cannot be a symlink to a template — launchd needs the real paths
# baked in — so render it.
sed -e "s|__HOME__|$HOME|g" \
    -e "s|__LABEL__|$PLIST_LABEL|g" \
    -e "s|__CONFIG_DIR__|$CLAUDE_DIR|g" \
    -e "s|__LOG__|$LOG_PATH|g" \
  "$REPO/launchd/com.verveguy.claude-dispatcher.plist.template" > "$PLIST_DEST"
say "rendered $PLIST_DEST"

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
