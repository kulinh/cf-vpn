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
! grep -RInE '(docker|docker-compose|container_name|docker compose)' "${DOCS[@]}"
