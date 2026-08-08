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
PLUGIN_DEST="$HOME/.claude/skills/topics"
PLIST_LABEL="com.verveguy.claude-dispatcher"
PLIST_DEST="$HOME/Library/LaunchAgents/$PLIST_LABEL.plist"

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
  for f in ccs topic; do
    [[ -L "$BIN/$f" ]] && { rm -f "$BIN/$f"; say "removed $BIN/$f"; }
  done
  [[ -L "$PLUGIN_DEST" ]] && { rm -f "$PLUGIN_DEST"; say "removed $PLUGIN_DEST"; }
  echo
  echo "Left in place (deliberately — this is your data, not the tool):"
  say "$HOME/.claude/topics/   topic registry + handoff documents"
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
mkdir -p "$BIN"
link "$REPO/bin/ccs"   "$BIN/ccs"
link "$REPO/bin/topic" "$BIN/topic"
link "$REPO/plugin"    "$PLUGIN_DEST"

# The plist cannot be a symlink to a template — launchd needs the real paths
# baked in — so render it.
sed "s|__HOME__|$HOME|g" \
  "$REPO/launchd/$PLIST_LABEL.plist.template" > "$PLIST_DEST"
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
