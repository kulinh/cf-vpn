#!/usr/bin/env bash
set -euo pipefail
! grep -q "docker compose up" README.md
grep -q "cfvpnctl install" README.md
grep -q "/etc/cfvpn/cfvpn.env" README.md
