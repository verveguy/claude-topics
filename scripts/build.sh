#!/usr/bin/env bash
#
# Build bin/topic — the only supported way to do it.
#
# Besides compiling, this gives the binary a stable code-signing identity on macOS.
# That is not cosmetic, and skipping it is the bug this script exists to prevent:
#
#   launchd starts `topic ensure-dispatcher` at login, before any terminal is open,
#   so `topic` is what creates the tmux server — which makes it the RESPONSIBLE
#   PROCESS for every Claude session, MCP server, hook and shell underneath it. Those
#   run as unsigned interpreters (node, sh, find), so macOS attributes their TCC
#   requests to topic's binary, and the permission dialog says "topic" would like to
#   access your photo library / other apps' data / all your files.
#
#   The Go linker signs ad-hoc with Identifier=a.out. That is not a unique identity —
#   every ad-hoc Go binary on the machine claims it — so TCC cannot persist a decision
#   against it. The stored answer stays "unknown" and the dialog comes back forever,
#   whether the user clicked Allow or Don't Allow.
#
# A stable identifier is what lets one answer stick. See README, "Why the binary is
# code-signed".
#
# Overrides:
#   TOPIC_IDENTIFIER     signing identifier (default: io.github.verveguy.topic)
#   TOPIC_SIGN_IDENTITY  codesign -s value; "-" is ad-hoc, which is enough for TCC.
#                        Set to a Developer ID to sign a binary for distribution.
#   TOPIC_OUT            output path (default: bin/topic)

set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${TOPIC_OUT:-$REPO/bin/topic}"
ID="${TOPIC_IDENTIFIER:-io.github.verveguy.topic}"
SIGN_IDENTITY="${TOPIC_SIGN_IDENTITY:--}"

command -v go >/dev/null || {
  echo "build: go not found on PATH — needed to build bin/topic" >&2
  exit 1
}

# -X stamps the identifier into the binary so it can tell, at runtime and without
# shelling out to codesign, that it was built through here. `topic doctor` reads it.
mkdir -p "$(dirname "$OUT")"
(cd "$REPO" && go build -ldflags "-X main.signedIdentifier=$ID" -o "$OUT" ./cmd/topic)

if [[ "$(uname -s)" != "Darwin" ]]; then
  # TCC is a macOS concept; elsewhere the build is simply done.
  echo "built $OUT"
  exit 0
fi

command -v codesign >/dev/null || {
  echo "build: codesign not found — install the Xcode command line tools." >&2
  echo "       Without it macOS will re-ask for permissions on every launch." >&2
  exit 1
}

codesign --force --sign "$SIGN_IDENTITY" --identifier "$ID" "$OUT"

# Verify rather than assume. A binary that silently kept Identifier=a.out is exactly
# the failure this script exists to prevent, so it must not exit 0.
got="$(codesign -d --verbose=2 "$OUT" 2>&1 | sed -n 's/^Identifier=//p')"
if [[ "$got" != "$ID" ]]; then
  echo "build: signing did not take — expected Identifier=$ID, got '${got:-none}'" >&2
  exit 1
fi

echo "built $OUT (Identifier=$got)"
