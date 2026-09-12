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

# cfvpn-tgbot moved to JPY-03 on 2026-09-12; VNM-01 only reaps its own
# fleet-probe alerts via /etc/cron.d/cfvpn-tgbot-reap. An operator who believes
# the README and looks for the bot on VNM-01 finds no process and no logs.
if grep -qiE 'cfvpn-tgbot.*VNM-01 only|VNM-01 only.*cfvpn-tgbot' README.md; then
  printf 'docs-assert: README still claims cfvpn-tgbot is VNM-01 only (it long-polls on JPY-03)\n' >&2
  exit 1
fi
