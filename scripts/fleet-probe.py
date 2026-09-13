#!/usr/bin/env python3
"""fleet-probe.py — end-to-end probe of every route in the subscription.

Runs on VNM-01 from cron. Builds one xray client (a SOCKS inbound per VLESS
route) plus one hysteria client per Hysteria2 route, fetches
http://cp.cloudflare.com/generate_204 through each, appends one line per
route to the log, keeps a consecutive-failure count per route, and posts to
Telegram when a route has failed FAIL_THRESHOLD times in a row (and once when
it recovers). The subscription itself is tracked the same way: a fetch error,
an empty body or duplicate route names alert once after FAIL_THRESHOLD runs
and once on recovery (exit 2, no route is probed). Nothing on the nodes is
touched.

    fleet-probe.py [--env /etc/cfvpn/fleet-probe.env] [--sub-file PATH] [--once]
    fleet-probe.py --reap [--env /etc/cfvpn/fleet-probe.env]

Env (file or process environment):
    SUB_URL             subscription URL (base64 body)
    TELEGRAM_BOT_TOKEN  empty = alerts are printed to stderr only (and --reap does nothing)
    TELEGRAM_CHAT_ID    default -1003806233980
    FAIL_THRESHOLD      default 2
    STATE_FILE          default /var/lib/cfvpn/fleet-probe.state
    LOG_FILE            default /var/log/cfvpn-fleet-probe.log
    XRAY_BIN / HYSTERIA_BIN   default /usr/local/bin/{xray,hysteria}
    TELEGRAM_TTL_DIR    default /var/lib/cfvpn/tg-ttl; empty = alerts are never auto-deleted
    TELEGRAM_MESSAGE_TTL_HOURS   default 24

--once: run a single pass and print the table; still logs and alerts.
--sub-file: read the subscription (base64 or decoded) from a file instead of SUB_URL.
--reap: delete the queued alert messages whose TTL passed (one pass, prints
        "reaped N", exits 0). scripts/fleet-probe-reap.cron runs it every 5 min.
"""
import argparse
import base64
import fcntl
import json
import os
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import traceback
import urllib.error
import urllib.parse as up
import urllib.request
from dataclasses import dataclass, field
from datetime import datetime, timezone

TARGET = "http://cp.cloudflare.com/generate_204"
BASE_PORT = 21000
CURL_TIMEOUT = "10"
TRIES = 2
RETRY_AFTER_MAX = 10.0  # seconds; longest we honour Telegram's 429 retry_after


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


def duplicate_names(routes: list) -> list:
    """Route names that occur more than once, sorted.

    Names become xray outbound tags and the per-route port map key; two routes
    sharing one would overwrite each other and xray rejects the duplicate tag,
    failing every VLESS route at once — so a duplicate is a subscription bug to
    report, not something to probe around.
    """
    seen = {}
    for r in routes:
        seen[r.name] = seen.get(r.name, 0) + 1
    return sorted(n for n, c in seen.items() if c > 1)


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
        # XHTTP-H3 URIs carry alpn=h3; without it xray dials TCP and the route
        # always reads as down.
        if q.get("alpn"):
            ss["tlsSettings"]["alpn"] = q["alpn"].split(",")
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
    cfg = {"server": (f"[{r.host}]" if ":" in r.host else r.host) + f":{r.port}",
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


# The subscription itself is tracked under this reserved key with the same
# consecutive-failure logic as a route: a dead panel (fetch error, empty or
# malformed body) must alert once after FAIL_THRESHOLD runs and once on
# recovery — before this, those paths only printed to stderr and returned 2,
# so a dead panel looked exactly like silence.
FETCH_KEY = "__fetch__"


def fold_fetch(prev: dict, ok: bool, threshold: int, reason: str = ""):
    """Fold one fetch outcome into prev[FETCH_KEY]; return (entry, alerts)."""
    sub = {FETCH_KEY: prev.get(FETCH_KEY, {"fails": 0, "alerted": False})}
    state, raw = next_state(sub, {FETCH_KEY: 0 if ok else None}, threshold)
    alerts = []
    for a in raw:
        if a.startswith("DOWN"):
            alerts.append(f"DOWN subscription: {reason} ({state[FETCH_KEY]['fails']} consecutive failures)")
        else:
            alerts.append("UP subscription: routes fetched again")
    return state[FETCH_KEY], alerts


def load_state(state_file: str) -> dict:
    if os.path.exists(state_file):
        try:
            return json.load(open(state_file))
        except Exception:
            pass
    return {}


def save_state(state_file: str, state: dict) -> None:
    tmp = state_file + ".tmp"
    json.dump(state, open(tmp, "w"))
    os.replace(tmp, state_file)


# ---------------------------------------------------------------- runtime

def load_env(path):
    env = dict(os.environ)
    if path and os.path.exists(path):
        for line in open(path):
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            k, v = line.split("=", 1)
            # Verbatim after the first '=', like internal/state/store.go and
            # janitor.py: the writers refuse quotes, so a reader that stripped
            # them would be the only one disagreeing about the value.
            env.setdefault(k.strip(), v.strip())
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


# Auto-delete: the ops group keeps nothing older than TTL. Telegram has no
# server-side TTL for bot messages and only lets a bot delete messages younger
# than 48 h, so every alert is queued as one JSON file per message in TTL["dir"]
# ({"chat_id","message_id","delete_at"} in unix seconds) and `--reap` (cron,
# every 5 minutes) deletes the due ones. One file per message means no locking
# between the writer and the reaper, and a queued deletion survives reboots.
TTL = {"dir": "/var/lib/cfvpn/tg-ttl", "hours": 24.0}
# Telegram refuses to delete anything older than this; a file past it is
# dropped after one last attempt so the queue cannot grow forever.
TELEGRAM_DELETE_WINDOW = 48 * 3600
# Telegram answers that will never change: already gone, too old, not ours.
# Kept in sync with PERMANENT_ERRORS in /opt/xiaoqie_bot/janitor.py.
PERMANENT_DELETE_ERRORS = (
    "message to delete not found",
    "message can't be deleted",
    "message identifier is not specified",
    "message_id_invalid",
    "message not found",
)


def configure_ttl(env: dict) -> None:
    if "TELEGRAM_TTL_DIR" in env:
        TTL["dir"] = env["TELEGRAM_TTL_DIR"].strip()  # empty = disabled
    if env.get("TELEGRAM_MESSAGE_TTL_HOURS", "").strip():
        TTL["hours"] = float(env["TELEGRAM_MESSAGE_TTL_HOURS"])


def record_ttl(ttl_dir: str, chat_id: str, message_id: int, hours: float, now: float | None = None) -> str | None:
    """Queue one message for deletion; returns the file written (None if disabled)."""
    if not ttl_dir or not message_id:
        return None
    now = time.time() if now is None else now
    os.makedirs(ttl_dir, mode=0o700, exist_ok=True)
    path = os.path.join(ttl_dir, f"{chat_id}_{message_id}.json")
    tmp = path + ".tmp"
    entry = {"chat_id": int(chat_id), "message_id": int(message_id), "delete_at": int(now + hours * 3600)}
    with open(tmp, "w") as f:
        json.dump(entry, f)
    os.chmod(tmp, 0o600)
    os.replace(tmp, path)
    return path


class TelegramError(Exception):
    """A Telegram API answer with ok:false (its description is the message)."""


def is_permanent_delete_error(err: Exception) -> bool:
    msg = str(err).lower()
    return any(needle in msg for needle in PERMANENT_DELETE_ERRORS)


def delete_telegram_message(token: str, chat_id: int, message_id: int) -> None:
    """One deleteMessage call. Raises TelegramError on an API refusal (with the
    description, so the caller can tell permanent from transient) and
    urllib.error.URLError / OSError on transport failures."""
    data = up.urlencode({"chat_id": chat_id, "message_id": message_id}).encode()
    req = urllib.request.Request(f"https://api.telegram.org/bot{token}/deleteMessage", data=data)
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            body = json.load(resp)
    except urllib.error.HTTPError as e:
        # Telegram puts the reason in the JSON body of a 4xx; keep it.
        try:
            desc = json.load(e).get("description") or ""
        except (ValueError, AttributeError):
            desc = ""
        raise TelegramError(f"{e.code} {desc or e.reason}") from None
    if not isinstance(body, dict) or not body.get("ok"):
        raise TelegramError(str(body.get("description") if isinstance(body, dict) else body))


def reap_once(token: str, ttl_dir: str, now: float | None = None) -> int:
    """Delete every queued message whose delete_at passed; returns how many were
    deleted. A file is removed after a successful delete, after a permanent
    Telegram refusal, or once it is older than Telegram's 48 h window; it is
    kept on a transient error (the next pass retries). Malformed files are
    removed. Does nothing without a token or a queue directory."""
    if not token or not ttl_dir or not os.path.isdir(ttl_dir):
        return 0
    now = time.time() if now is None else now
    deleted = 0
    for name in sorted(os.listdir(ttl_dir)):
        path = os.path.join(ttl_dir, name)
        if not name.endswith(".json") or not os.path.isfile(path):
            continue
        try:
            with open(path) as f:
                entry = json.load(f)
            chat_id, message_id = int(entry["chat_id"]), int(entry["message_id"])
            delete_at = int(entry.get("delete_at") or 0)
        except (OSError, ValueError, TypeError, KeyError, AttributeError):
            print(f"ttl: dropping unreadable {name}", file=sys.stderr)
            _remove_quietly(path)
            continue
        # Unix epoch 0 is "due since 1970": a file without delete_at (another
        # tool's layout) would be deleted the moment it was queued. Drop it.
        if not chat_id or not message_id or delete_at <= 0:
            print(f"ttl: dropping {name}: no chat_id/message_id/delete_at", file=sys.stderr)
            _remove_quietly(path)
            continue
        if now < delete_at:
            continue
        try:
            delete_telegram_message(token, chat_id, message_id)
        except (TelegramError, urllib.error.URLError, OSError) as e:
            if is_permanent_delete_error(e):
                print(f"ttl: message {message_id} already gone or undeletable ({e}); dropping", file=sys.stderr)
                _remove_quietly(path)
            elif now - delete_at > TELEGRAM_DELETE_WINDOW:
                print(f"ttl: message {message_id} past Telegram's 48 h window ({e}); dropping", file=sys.stderr)
                _remove_quietly(path)
            else:
                print(f"ttl: delete {message_id}: {e} (will retry)", file=sys.stderr)
            continue
        deleted += 1
        _remove_quietly(path)
    return deleted


def _remove_quietly(path: str) -> None:
    try:
        os.remove(path)
    except OSError:
        pass


def probe_label(env: dict) -> str:
    """Name of this vantage point in alerts: PROBE_LABEL, else the hostname."""
    return (env.get("PROBE_LABEL") or "").strip() or socket.gethostname().lower()


def send_telegram(token: str, chat_id: str, text: str) -> bool:
    if not token:
        print("telegram: TELEGRAM_BOT_TOKEN empty; alert not sent:\n" + text, file=sys.stderr)
        return False
    data = up.urlencode({"chat_id": chat_id, "text": text}).encode()
    req = urllib.request.Request(f"https://api.telegram.org/bot{token}/sendMessage", data=data)
    # One retry on 429: Telegram says how long to wait in parameters.retry_after
    # (seconds). Capped so a cron run never hangs on an absurd value.
    for attempt in range(2):
        try:
            with urllib.request.urlopen(req, timeout=15) as resp:
                if resp.status != 200:
                    return False
                try:
                    message_id = json.load(resp).get("result", {}).get("message_id")
                except (ValueError, AttributeError):
                    message_id = None
            break
        except urllib.error.HTTPError as e:
            retry_after = None
            if e.code == 429 and attempt == 0:
                try:
                    retry_after = float(json.load(e).get("parameters", {}).get("retry_after"))
                except (ValueError, TypeError, AttributeError):
                    retry_after = None
            if retry_after is None:
                print(f"telegram: send failed: {e}", file=sys.stderr)
                return False
            time.sleep(min(retry_after, RETRY_AFTER_MAX))
        except urllib.error.URLError as e:
            print(f"telegram: send failed: {e}", file=sys.stderr)
            return False
    try:
        record_ttl(TTL["dir"], chat_id, message_id, TTL["hours"])
    except OSError as e:  # a lost auto-delete must never turn into a lost alert
        print(f"telegram: ttl queue: {e}", file=sys.stderr)
    return True


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--env", default="/etc/cfvpn/fleet-probe.env")
    ap.add_argument("--sub-file")
    ap.add_argument("--once", action="store_true", help="print the result table to stdout")
    ap.add_argument("--reap", action="store_true", help="delete the queued alert messages whose TTL passed, then exit")
    args = ap.parse_args(argv)
    env = load_env(args.env)
    configure_ttl(env)
    if args.reap:
        print(f"reaped {reap_once(env.get('TELEGRAM_BOT_TOKEN', ''), TTL['dir'])}")
        return 0
    state_file = env.get("STATE_FILE", "/var/lib/cfvpn/fleet-probe.state")
    log_file = env.get("LOG_FILE", "/var/log/cfvpn-fleet-probe.log")
    threshold = int(env.get("FAIL_THRESHOLD", "2"))
    xray_bin = env.get("XRAY_BIN", "/usr/local/bin/xray")
    hy_bin = env.get("HYSTERIA_BIN", "/usr/local/bin/hysteria")

    token, chat_id = env.get("TELEGRAM_BOT_TOKEN", ""), env.get("TELEGRAM_CHAT_ID", "-1003806233980")
    # Two probes run (home box + a datacenter node); the label tells which
    # vantage point saw the failure: both = the node is down, one = its path.
    header = f"cfvpn fleet-probe @{probe_label(env)}\n"

    os.makedirs(os.path.dirname(state_file), exist_ok=True)
    lock = open(state_file + ".lock", "w")
    fcntl.flock(lock, fcntl.LOCK_EX)  # overlapping cron runs would race on ports and state
    prev = load_state(state_file)

    def fetch_failed(reason: str) -> int:
        print(reason, file=sys.stderr)
        prev[FETCH_KEY], alerts = fold_fetch(prev, False, threshold, reason)
        save_state(state_file, prev)
        if alerts:
            send_telegram(token, chat_id, header + "\n".join(alerts))
        return 2

    if args.sub_file:
        text = open(args.sub_file).read()
    else:
        url = env.get("SUB_URL", "")
        if not url:
            print("SUB_URL is not set (and no --sub-file)", file=sys.stderr)
            return 2
        try:
            text = fetch_subscription(url)
        except Exception as e:  # a dead panel is itself an alert-worthy event
            return fetch_failed(f"fetch subscription failed: {e}")
    routes = parse_subscription(text)
    if not routes:
        return fetch_failed("no routes in subscription")
    dups = duplicate_names(routes)
    if dups:
        return fetch_failed("duplicate route names in subscription: " + ", ".join(dups))
    fetch_entry, fetch_alerts = fold_fetch(prev, True, threshold)

    # A missing xray/hysteria binary or a full disk used to leave only a
    # traceback in the cron .err file: the probe went silent with no alert.
    try:
        results = run_probes(routes, xray_bin, hy_bin)
        ts = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
        with open(log_file, "a") as lf:
            for name, ms in results.items():
                lf.write(f"{ts} {name} {'OK' if ms is not None else 'FAIL'} {ms if ms is not None else '-'}\n")
    except Exception as e:
        traceback.print_exc()
        send_telegram(token, chat_id, header + f"probe run crashed: {type(e).__name__}: {e}")
        return 2

    state, alerts = next_state(prev, results, threshold)
    state[FETCH_KEY] = fetch_entry  # next_state keeps only probed routes
    save_state(state_file, state)

    alerts = fetch_alerts + alerts
    if alerts:
        send_telegram(token, chat_id, header + "\n".join(alerts))

    if args.once:
        for name, ms in results.items():
            print(f"{name:<30} {'OK' if ms is not None else 'FAIL':<5} {ms if ms is not None else '-'}")
    failed = [n for n, ms in results.items() if ms is None]
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
