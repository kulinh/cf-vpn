"""Unit tests for scripts/fleet-probe.py (pure functions only; no network).

    python3 -m pytest scripts/tests -q
"""
import base64
import importlib.util
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location("fleet_probe", os.path.join(HERE, "..", "fleet-probe.py"))
fp = importlib.util.module_from_spec(spec)
sys.modules["fleet_probe"] = fp
spec.loader.exec_module(fp)

SAMPLE = "\n".join([
    "REMARKS=RWL8899",
    "vless://11111111-2222-4333-8444-555555555555@96.9.231.74:443?encryption=none&security=reality"
    "&flow=xtls-rprx-vision&type=tcp&sni=www.singaporeair.com&pbk=PBK&sid=4a2739d7c27cf56d&fp=chrome#kulinh%40SIN-01-Reality",
    "vless://11111111-2222-4333-8444-555555555555@static-df60bd79.duylinh.org:443?encryption=none&security=tls"
    "&type=httpupgrade&host=static-df60bd79.duylinh.org&path=%2Fapi%2Fv1%2Fsync&sni=static-df60bd79.duylinh.org#kulinh%40OR-001-HTTPUpgrade",
    "hysteria2://kulinh:secretpw@hy-c36ca6bd.dongnat247.com:31300/?obfs=salamander&obfs-password=obfspw"
    "&sni=hy-c36ca6bd.dongnat247.com&insecure=0#kulinh%40HKG-01-HY2",
])


def test_parse_decoded_text_in_order():
    routes = fp.parse_subscription(SAMPLE)
    assert [r.kind for r in routes] == ["vless", "vless", "hy2"]
    assert [r.name for r in routes] == ["kulinh@SIN-01-Reality", "kulinh@OR-001-HTTPUpgrade", "kulinh@HKG-01-HY2"]
    assert routes[0].host == "96.9.231.74" and routes[0].port == 443


def test_parse_base64_body():
    b64 = base64.b64encode(SAMPLE.encode()).decode()
    assert [r.name for r in fp.parse_subscription(b64)] == [r.name for r in fp.parse_subscription(SAMPLE)]


def test_reality_outbound():
    ob = fp.xray_outbound(fp.parse_subscription(SAMPLE)[0])
    ss = ob["streamSettings"]
    assert ss["security"] == "reality"
    assert ss["realitySettings"] == {"serverName": "www.singaporeair.com", "publicKey": "PBK",
                                     "shortId": "4a2739d7c27cf56d", "fingerprint": "chrome"}
    assert ob["settings"]["vnext"][0]["users"][0]["flow"] == "xtls-rprx-vision"
    assert ob["settings"]["vnext"][0]["address"] == "96.9.231.74"


def test_httpupgrade_outbound_decodes_path():
    ob = fp.xray_outbound(fp.parse_subscription(SAMPLE)[1])
    ss = ob["streamSettings"]
    assert ss["security"] == "tls" and ss["network"] == "httpupgrade"
    assert ss["httpupgradeSettings"] == {"path": "/api/v1/sync", "host": "static-df60bd79.duylinh.org"}


def test_hy2_auth_is_user_colon_password():
    cfg = fp.hysteria_config(fp.parse_subscription(SAMPLE)[2], 21003)
    assert cfg["auth"] == "kulinh:secretpw"
    assert cfg["obfs"]["salamander"]["password"] == "obfspw"
    assert cfg["socks5"]["listen"] == "127.0.0.1:21003"
    assert cfg["tls"] == {"sni": "hy-c36ca6bd.dongnat247.com", "insecure": False}


def test_alert_on_second_consecutive_failure_and_on_recovery():
    s, a = fp.next_state({}, {"A": None}, 2)
    assert a == [] and s["A"] == {"fails": 1, "alerted": False}
    s, a = fp.next_state(s, {"A": None}, 2)
    assert a == ["DOWN A (2 consecutive failures)"] and s["A"]["alerted"] is True
    s, a = fp.next_state(s, {"A": None}, 2)
    assert a == [] and s["A"]["fails"] == 3  # no repeat spam
    s, a = fp.next_state(s, {"A": 120}, 2)
    assert a == ["UP A (120 ms)"] and s["A"] == {"fails": 0, "alerted": False}


def test_single_failure_then_success_is_silent():
    s, a = fp.next_state({}, {"A": None, "B": 50}, 2)
    s, a = fp.next_state(s, {"A": 80, "B": 60}, 2)
    assert a == [] and s["A"]["fails"] == 0


def test_routes_missing_from_this_run_are_dropped_from_state():
    s, _ = fp.next_state({"GONE": {"fails": 5, "alerted": True}}, {"A": 10}, 2)
    assert "GONE" not in s


def test_record_ttl_writes_one_json_file_per_message(tmp_path):
    import json
    import os
    path = fp.record_ttl(str(tmp_path / "q"), "-1003806233980", 4242, 24, now=1_789_300_800)
    assert path == str(tmp_path / "q" / "-1003806233980_4242.json")
    assert json.load(open(path)) == {"chat_id": -1003806233980, "message_id": 4242, "delete_at": 1_789_300_800 + 24 * 3600}
    assert oct(os.stat(path).st_mode & 0o777) == "0o600"
    assert not [p for p in os.listdir(tmp_path / "q") if p.endswith(".tmp")]
    # Disabled (empty dir) or no message id: nothing written, no error.
    assert fp.record_ttl("", "-1", 1, 24) is None
    assert fp.record_ttl(str(tmp_path / "q"), "-1", None, 24) is None


def test_configure_ttl_reads_env(monkeypatch):
    fp.configure_ttl({"TELEGRAM_TTL_DIR": "", "TELEGRAM_MESSAGE_TTL_HOURS": "0.5"})
    assert fp.TTL == {"dir": "", "hours": 0.5}
    fp.configure_ttl({"TELEGRAM_TTL_DIR": "/tmp/x", "TELEGRAM_MESSAGE_TTL_HOURS": ""})
    assert fp.TTL["dir"] == "/tmp/x" and fp.TTL["hours"] == 0.5
    fp.TTL.update({"dir": "/var/lib/cfvpn/tg-ttl", "hours": 24.0})


def test_probe_label_prefers_env_then_hostname(monkeypatch):
    assert fp.probe_label({"PROBE_LABEL": " JPY-03 "}) == "JPY-03"
    monkeypatch.setattr(fp.socket, "gethostname", lambda: "VNM-01")
    assert fp.probe_label({}) == "vnm-01"
    assert fp.probe_label({"PROBE_LABEL": ""}) == "vnm-01"


def test_load_env_keeps_quotes_verbatim(tmp_path):
    """Every other reader of these files (internal/state/store.go, janitor.py)
    keeps the text after the first '=' as is; the probe must not be the one
    reader that silently strips quotes."""
    envf = tmp_path / "probe.env"
    envf.write_text('# c\nA="quoted"\nB=\'single\'\nC=x=y\n\nD = spaced \n')
    env = fp.load_env(str(envf))
    assert env["A"] == '"quoted"' and env["B"] == "'single'" and env["C"] == "x=y" and env["D"] == "spaced"


def test_duplicate_route_names_are_detected():
    dup = SAMPLE + "\nvless://11111111-2222-4333-8444-555555555555@1.2.3.4:443?security=reality&sni=a&pbk=B#kulinh%40SIN-01-Reality"
    assert fp.duplicate_names(fp.parse_subscription(SAMPLE)) == []
    assert fp.duplicate_names(fp.parse_subscription(dup)) == ["kulinh@SIN-01-Reality"]


def _probe_env(tmp_path, **extra):
    envf = tmp_path / "probe.env"
    kv = {"SUB_URL": "https://panel/sub/x", "STATE_FILE": str(tmp_path / "st" / "probe.state"),
          "LOG_FILE": str(tmp_path / "probe.log"), "FAIL_THRESHOLD": "2", "TELEGRAM_BOT_TOKEN": "t",
          "TELEGRAM_TTL_DIR": "", "PROBE_LABEL": "test-box"}
    kv.update(extra)
    envf.write_text("".join(f"{k}={v}\n" for k, v in kv.items()))
    return str(envf)


def _record_alerts(monkeypatch):
    sent = []
    monkeypatch.setattr(fp, "send_telegram", lambda token, chat, text: sent.append(text) or True)
    monkeypatch.setattr(fp, "run_probes", lambda routes, x, h: {r.name: 50 for r in routes})
    return sent


def test_fetch_failure_alerts_once_after_threshold_and_once_on_recovery(tmp_path, monkeypatch):
    """A dead panel used to print to stderr and exit 2 — indistinguishable from
    silence. It must alert like a route: once after FAIL_THRESHOLD, once on UP."""
    import json
    sent = _record_alerts(monkeypatch)
    envf = _probe_env(tmp_path)

    def boom(url):
        raise OSError("connection refused")
    monkeypatch.setattr(fp, "fetch_subscription", boom)
    assert fp.main(["--env", envf]) == 2
    assert sent == []                                   # 1st failure: below threshold
    assert fp.main(["--env", envf]) == 2
    assert len(sent) == 1 and sent[0].startswith("cfvpn fleet-probe @test-box\n")
    assert "DOWN subscription: fetch subscription failed: connection refused (2 consecutive failures)" in sent[0]
    assert fp.main(["--env", envf]) == 2
    assert len(sent) == 1                               # no repeat spam
    state = json.load(open(tmp_path / "st" / "probe.state"))
    assert state[fp.FETCH_KEY] == {"fails": 3, "alerted": True}

    monkeypatch.setattr(fp, "fetch_subscription", lambda url: SAMPLE)
    assert fp.main(["--env", envf]) == 0
    assert len(sent) == 2 and "UP subscription: routes fetched again" in sent[1]
    state = json.load(open(tmp_path / "st" / "probe.state"))
    assert state[fp.FETCH_KEY] == {"fails": 0, "alerted": False}
    assert state["kulinh@SIN-01-Reality"] == {"fails": 0, "alerted": False}
    assert fp.main(["--env", envf]) == 0
    assert len(sent) == 2                               # healthy run stays quiet


def test_empty_and_duplicate_subscriptions_alert_and_exit_2(tmp_path, monkeypatch):
    sent = _record_alerts(monkeypatch)
    envf = _probe_env(tmp_path, FAIL_THRESHOLD="1")
    monkeypatch.setattr(fp, "fetch_subscription", lambda url: "REMARKS=RWL8899\n")
    assert fp.main(["--env", envf]) == 2
    assert len(sent) == 1 and "DOWN subscription: no routes in subscription (1 consecutive failures)" in sent[0]

    dup = SAMPLE + "\nvless://11111111-2222-4333-8444-555555555555@1.2.3.4:443?security=tls#kulinh%40SIN-01-Reality"
    monkeypatch.setattr(fp, "fetch_subscription", lambda url: dup)
    assert fp.main(["--env", envf]) == 2                # already alerted: exit 2, no new message
    assert len(sent) == 1
    monkeypatch.setattr(fp, "fetch_subscription", lambda url: SAMPLE)
    assert fp.main(["--env", envf]) == 0
    assert len(sent) == 2 and "UP subscription" in sent[1]
    monkeypatch.setattr(fp, "fetch_subscription", lambda url: dup)
    assert fp.main(["--env", envf]) == 2
    assert "duplicate route names in subscription: kulinh@SIN-01-Reality" in sent[2]


def test_send_telegram_honours_429_retry_after(monkeypatch):
    import io
    import urllib.error
    calls, slept = [], []

    class Resp(io.BytesIO):
        status = 200

        def __enter__(self):
            return self

        def __exit__(self, *a):
            return False

    def fake_urlopen(req, timeout=0):
        calls.append(req)
        if len(calls) == 1:
            raise urllib.error.HTTPError(req.full_url, 429, "Too Many Requests", {},
                                         io.BytesIO(b'{"ok":false,"parameters":{"retry_after":3}}'))
        return Resp(b'{"ok":true,"result":{"message_id":7}}')

    monkeypatch.setattr(fp.urllib.request, "urlopen", fake_urlopen)
    monkeypatch.setattr(fp.time, "sleep", lambda s: slept.append(s))
    monkeypatch.setitem(fp.TTL, "dir", "")
    assert fp.send_telegram("tok", "-1", "hi") is True
    assert len(calls) == 2 and slept == [3.0]

    # A second 429 is not retried again; an oversized retry_after is capped.
    calls.clear(); slept.clear()

    def always429(req, timeout=0):
        calls.append(req)
        raise urllib.error.HTTPError(req.full_url, 429, "x", {}, io.BytesIO(b'{"parameters":{"retry_after":900}}'))
    monkeypatch.setattr(fp.urllib.request, "urlopen", always429)
    assert fp.send_telegram("tok", "-1", "hi") is False
    assert len(calls) == 2 and slept == [fp.RETRY_AFTER_MAX]

    # Other HTTP errors fail immediately without sleeping.
    calls.clear(); slept.clear()

    def e400(req, timeout=0):
        calls.append(req)
        raise urllib.error.HTTPError(req.full_url, 400, "Bad Request", {}, io.BytesIO(b'{}'))
    monkeypatch.setattr(fp.urllib.request, "urlopen", e400)
    assert fp.send_telegram("tok", "-1", "hi") is False
    assert len(calls) == 1 and slept == []


def test_h3_outbound_carries_alpn():
    text = ("vless://u@quic.example.com:443?encryption=none&security=tls&type=xhttp&host=quic.example.com"
            "&path=%2Fp&mode=stream-one&alpn=h3&sni=quic.example.com#kulinh%40JPY-03-XHTTP-H3")
    (r,) = fp.parse_subscription(text)
    assert fp.xray_outbound(r)["streamSettings"]["tlsSettings"]["alpn"] == ["h3"]


def test_ipv6_routes_parse_and_hysteria_server_is_bracketed():
    text = ("vless://u@[2603:c023::1]:443?security=reality&type=tcp&sni=www.sony.jp&pbk=k&sid=s#a-Reality-v6\n"
            "hysteria2://kulinh:pw@[2603:c023::1]:32443/?obfs=salamander&obfs-password=o&sni=q.example.com#a-HY2-v6")
    v, h = fp.parse_subscription(text)
    assert fp.xray_outbound(v)["settings"]["vnext"][0]["address"] == "2603:c023::1"
    assert fp.hysteria_config(h, 21001)["server"] == "[2603:c023::1]:32443"
