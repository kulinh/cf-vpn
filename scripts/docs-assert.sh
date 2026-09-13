#!/usr/bin/env bash
# docs-assert.sh — guard rails on the docs. Run from anywhere.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

DOCS=(README.md docs/TESTING.md docs/INSTALL_MINIMAL.md)

# `! grep -RInE ... file` used to pass when a file was missing: grep exits 2,
# and `!` turned that into success — renaming a doc silently disabled the
# assertions instead of failing them.
for f in "${DOCS[@]}"; do
  [ -f "$f" ] || { printf 'docs-assert: missing %s\n' "$f" >&2; exit 1; }
done

grep -q "cfvpnctl install" README.md
grep -q "/etc/cfvpn/cfvpn.env" README.md
# `! grep ...` as a bare command skips errexit, so once another assertion follows
# it a docker mention would be found and then ignored. Check it explicitly.
if grep -RInE '(docker|docker-compose|container_name|docker compose)' "${DOCS[@]}"; then
  printf 'docs-assert: the docs above mention docker; this project does not use it\n' >&2
  exit 1
fi

# The Telegram control bot and `cfvpnctl rules-mode` were removed on
# 2026-09-13 (DERP china-mode stays on permanently; the blocked-site list is
# chosen per link with ?rules=). A README that still tells the operator to
# build, install or drive them sends them to a binary that no longer exists.
if grep -nE 'cfvpn-tgbot|rules-mode' README.md; then
  printf 'docs-assert: README still mentions the removed Telegram control bot / rules-mode (see above)\n' >&2
  exit 1
fi
# ...and it must say how the probe alerts get deleted now.
grep -q 'fleet-probe-reap.cron' README.md
