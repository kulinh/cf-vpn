#!/usr/bin/env python3
"""fleet-probe.py — end-to-end probe of every route in the subscription.

Runs on VNM-01 from cron. Builds one xray client (a SOCKS inbound per VLESS
route) plus one hysteria client per Hysteria2 route, fetches
http://cp.cloudflare.com/generate_204 through each, appends one line per
route to the log, keeps a consecutive-failure count per route, and posts to
Telegram when a route has failed FAIL_THRESHOLD times in a row (and once when
it recovers). Nothing on the nodes is touched.

    fleet-probe.py [--env /etc/cfvpn/fleet-probe.env] [--sub-file PATH] [--once]

Env (file or process environment):
    SUB_URL             subscription URL (base64 body)
    TELEGRAM_BOT_TOKEN  empty = alerts are printed to stderr only
    TELEGRAM_CHAT_ID    default -1003806233980
    FAIL_THRESHOLD      default 2
    STATE_FILE          default /var/lib/cfvpn/fleet-probe.state
    LOG_FILE            default /var/log/cfvpn-fleet-probe.log
    XRAY_BIN / HYSTERIA_BIN   default /usr/local/bin/{xray,hysteria}

--once: run a single pass and print the table; still logs and alerts.
--sub-file: read the subscription (base64 or decoded) from a file instead of SUB_URL.
"""
import argparse
import base64
import fcntl
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse as up
import urllib.request
from dataclasses import dataclass, field
from datetime import datetime, timezone

TARGET = "http://cp.cloudflare.com/generate_204"
BASE_PORT = 21000
CURL_TIMEOUT = "10"
TRIES = 2


@dataclass
class Route:
    name: str
    kind: str  # "vless" | "hy2"
    host: str
    port: int
    user: str
    password: str  # hy2 password, "" for vless
    query: dict = field(default_factory=dict)


# ---------------------------------------------------------------- parsing

def parse_subscription(text: str) -> list:
    """Accept a base64 body or the decoded URI list; return Routes in order."""
    text = text.strip()
    if text and "://" not in text.split("\n", 1)[0] and not text.startswith("REMARKS="):
        try:
            text = base64.b64decode(text + "=" * (-len(text) % 4)).decode("utf-8", "replace")
        except Exception:
            pass
    routes = []
    for line in text.splitlines():
        line = line.strip()
        if not line or line.startswith("REMARKS=") or not line.startswith(("vless://", "hysteria2://")):
            continue
        u = up.urlsplit(line)
        q = dict(up.parse_qsl(u.query))
        name = up.unquote(u.fragment) or f"{u.hostname}:{u.port}"
        if u.scheme == "vless":
            routes.append(Route(name, "vless", u.hostname, u.port or 443, u.username or "", "", q))
        else:
            routes.append(Route(name, "hy2", u.hostname, u.port or 443, up.unquote(u.username or ""),
                                up.unquote(u.password or ""), q))
    return routes


def xray_outbound(r: Route) -> dict:
    q = r.query
    ss = {"network": q.get("type", "tcp")}
    if q.get("security") == "reality":
        ss["security"] = "reality"
        ss["realitySettings"] = {"serverName": q["sni"], "publicKey": q["pbk"],
                                 "shortId": q.get("sid", ""), "fingerprint": q.get("fp", "chrome")}
    elif q.get("security") == "tls":
        ss["security"] = "tls"
        ss["tlsSettings"] = {"serverName": q.get("sni", r.host)}
    if q.get("type") == "httpupgrade":
        ss["httpupgradeSettings"] = {"path": q.get("path", "/"), "host": q.get("host", r.host)}
    elif q.get("type") == "xhttp":
        ss["xhttpSettings"] = {"path": q.get("path", "/"), "host": q.get("host", r.host), "mode": q.get("mode", "auto")}
    user = {"id": r.user, "encryption": "none"}
    if q.get("flow"):
        user["flow"] = q["flow"]
    return {"tag": r.name, "protocol": "vless",
            "settings": {"vnext": [{"address": r.host, "port": r.port, "users": [user]}]},
            "streamSettings": ss}


def hysteria_config(r: Route, socks_port: int) -> dict:
    q = r.query
    cfg = {"server": f"{r.host}:{r.port}",
           # server runs auth.type userpass: the client must send user:password
           "auth": f"{r.user}:{r.password}",
           "tls": {"sni": q.get("sni", r.host), "insecure": q.get("insecure") == "1"},
           "socks5": {"listen": f"127.0.0.1:{socks_port}"},
           "lazy": True}
    if q.get("obfs") == "salamander":
        cfg["obfs"] = {"type": "salamander", "salamander": {"password": q.get("obfs-password", "")}}
    return cfg


# ---------------------------------------------------------------- state

def next_state(prev: dict, results: dict, threshold: int):
    """Fold this run's results into the state; return (state, alerts).

    results: {route: latency_ms or None}. A route alerts DOWN on its
    threshold-th consecutive failure (once), and UP on the first success
    after having alerted.
    """
    state = {}
    alerts = []
    for name, ms in results.items():
        p = prev.get(name, {"fails": 0, "alerted": False})
        if ms is None:
            fails = p["fails"] + 1
            alerted = p["alerted"]
            if fails >= threshold and not alerted:
                alerts.append(f"DOWN {name} ({fails} consecutive failures)")
                alerted = True
            state[name] = {"fails": fails, "alerted": alerted}
        else:
            if p["alerted"]:
                alerts.append(f"UP {name} ({ms} ms)")
            state[name] = {"fails": 0, "alerted": False}
    return state, alerts


# ---------------------------------------------------------------- runtime

def load_env(path):
    env = dict(os.environ)
    if path and os.path.exists(path):
        for line in open(path):
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            k, v = line.split("=", 1)
            env.setdefault(k.strip(), v.strip().strip('"').strip("'"))
    return env


def fetch_subscription(url: str) -> str:
    req = urllib.request.Request(url, headers={"User-Agent": "cfvpn-fleet-probe/1"})
    with urllib.request.urlopen(req, timeout=20) as resp:
        return resp.read().decode("utf-8", "replace")


def probe_port(port: int) -> int | None:
    best = None
    for _ in range(TRIES):
        r = subprocess.run(["curl", "-s", "-o", "/dev/null", "-w", "%{http_code} %{time_total}",
                            "--socks5-hostname", f"127.0.0.1:{port}", "--max-time", CURL_TIMEOUT, TARGET],
                           capture_output=True, text=True)
        parts = r.stdout.split()
        if len(parts) == 2 and parts[0] == "204":
            ms = int(float(parts[1]) * 1000)
            best = ms if best is None else min(best, ms)
    return best


def run_probes(routes: list, xray_bin: str, hy_bin: str) -> dict:
    work = tempfile.mkdtemp(prefix="fleet-probe-")
    procs = []
    ports = {}
    try:
        port = BASE_PORT
        vless = [r for r in routes if r.kind == "vless"]
        hy2 = [r for r in routes if r.kind == "hy2"]
        for r in routes:
            port += 1
            ports[r.name] = port
        if vless:
            cfg = {"log": {"loglevel": "warning"},
                   "inbounds": [{"tag": f"in-{r.name}", "listen": "127.0.0.1", "port": ports[r.name],
                                 "protocol": "socks", "settings": {"udp": False}} for r in vless],
                   "outbounds": [xray_outbound(r) for r in vless] + [{"tag": "direct", "protocol": "freedom"}],
                   "routing": {"rules": [{"type": "field", "inboundTag": [f"in-{r.name}"], "outboundTag": r.name} for r in vless]}}
            p = os.path.join(work, "xray.json")
            json.dump(cfg, open(p, "w"))
            procs.append(subprocess.Popen([xray_bin, "run", "-c", p], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL))
        for r in hy2:
            p = os.path.join(work, f"hy-{ports[r.name]}.json")
            json.dump(hysteria_config(r, ports[r.name]), open(p, "w"))
            procs.append(subprocess.Popen([hy_bin, "client", "-c", p], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL))
        time.sleep(3)
        return {r.name: probe_port(ports[r.name]) for r in routes}
    finally:
        for p in procs:
            p.terminate()
        for p in procs:
            try:
                p.wait(timeout=5)
            except subprocess.TimeoutExpired:
                p.kill()
        shutil.rmtree(work, ignore_errors=True)


# The ops group keeps nothing older than a day, but this script does not do the
# deleting: the janitor bot (@xiaoqie001_bot, /opt/xiaoqie_bot) owns the policy
# and the deleteMessage calls for every bot in the group. Here we only note each
# alert we posted — one JSON file per message, so no locking is needed between
# the writers (cfvpn-tgbot writes the same directory).
SPOOL = {"dir": "/var/lib/xiaoqie-janitor/spool"}


def configure_spool(env: dict) -> None:
    if "TELEGRAM_SPOOL_DIR" in env:
        SPOOL["dir"] = env["TELEGRAM_SPOOL_DIR"].strip()  # empty = disabled


def record_sent(spool_dir: str, chat_id: str, message_id: int, now: float | None = None) -> str | None:
    """Note one sent message for the janitor; returns the file written (None if disabled)."""
    if not spool_dir or not message_id:
        return None
    now = time.time() if now is None else now
    os.makedirs(spool_dir, mode=0o700, exist_ok=True)
    path = os.path.join(spool_dir, f"{chat_id}_{message_id}.json")
    tmp = path + ".tmp"
    entry = {
        "chat_id": int(chat_id),
        "message_id": int(message_id),
        "source": "rwl_vpn_bot",
        "kind": "alert",
        "sent_at": int(now),
    }
    with open(tmp, "w") as f:
        json.dump(entry, f)
    os.chmod(tmp, 0o600)
    os.replace(tmp, path)
    return path


def send_telegram(token: str, chat_id: str, text: str) -> bool:
    if not token:
        print("telegram: TELEGRAM_BOT_TOKEN empty; alert not sent:\n" + text, file=sys.stderr)
        return False
    data = up.urlencode({"chat_id": chat_id, "text": text}).encode()
    req = urllib.request.Request(f"https://api.telegram.org/bot{token}/sendMessage", data=data)
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            if resp.status != 200:
                return False
            try:
                message_id = json.load(resp).get("result", {}).get("message_id")
            except (ValueError, AttributeError):
                message_id = None
    except urllib.error.URLError as e:
        print(f"telegram: send failed: {e}", file=sys.stderr)
        return False
    try:
        record_sent(SPOOL["dir"], chat_id, message_id)
    except OSError as e:  # a lost hand-off must never turn into a lost alert
        print(f"telegram: spool: {e}", file=sys.stderr)
    return True


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--env", default="/etc/cfvpn/fleet-probe.env")
    ap.add_argument("--sub-file")
    ap.add_argument("--once", action="store_true", help="print the result table to stdout")
    args = ap.parse_args(argv)
    env = load_env(args.env)
    configure_spool(env)
    state_file = env.get("STATE_FILE", "/var/lib/cfvpn/fleet-probe.state")
    log_file = env.get("LOG_FILE", "/var/log/cfvpn-fleet-probe.log")
    threshold = int(env.get("FAIL_THRESHOLD", "2"))
    xray_bin = env.get("XRAY_BIN", "/usr/local/bin/xray")
    hy_bin = env.get("HYSTERIA_BIN", "/usr/local/bin/hysteria")

    if args.sub_file:
        text = open(args.sub_file).read()
    else:
        url = env.get("SUB_URL", "")
        if not url:
            print("SUB_URL is not set (and no --sub-file)", file=sys.stderr)
            return 2
        try:
            text = fetch_subscription(url)
        except Exception as e:  # network failure fetching the sub is itself an alert-worthy event
            print(f"fetch subscription failed: {e}", file=sys.stderr)
            return 2
    routes = parse_subscription(text)
    if not routes:
        print("no routes in subscription", file=sys.stderr)
        return 2

    os.makedirs(os.path.dirname(state_file), exist_ok=True)
    lock = open(state_file + ".lock", "w")
    fcntl.flock(lock, fcntl.LOCK_EX)  # overlapping cron runs would race on ports and state

    results = run_probes(routes, xray_bin, hy_bin)
    ts = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    with open(log_file, "a") as lf:
        for name, ms in results.items():
            lf.write(f"{ts} {name} {'OK' if ms is not None else 'FAIL'} {ms if ms is not None else '-'}\n")

    prev = {}
    if os.path.exists(state_file):
        try:
            prev = json.load(open(state_file))
        except Exception:
            prev = {}
    state, alerts = next_state(prev, results, threshold)
    tmp = state_file + ".tmp"
    json.dump(state, open(tmp, "w"))
    os.replace(tmp, state_file)

    if alerts:
        send_telegram(env.get("TELEGRAM_BOT_TOKEN", ""), env.get("TELEGRAM_CHAT_ID", "-1003806233980"),
                      "cfvpn fleet-probe @vnm-01\n" + "\n".join(alerts))

    if args.once:
        for name, ms in results.items():
            print(f"{name:<30} {'OK' if ms is not None else 'FAIL':<5} {ms if ms is not None else '-'}")
    failed = [n for n, ms in results.items() if ms is None]
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
