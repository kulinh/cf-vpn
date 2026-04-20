#!/usr/bin/env bash
set -euo pipefail
grep -q "cfvpnctl install" README.md
grep -q "/etc/cfvpn/cfvpn.env" README.md
! grep -RInE '(docker|docker-compose|container_name|docker compose)' README.md docs/TESTING.md
