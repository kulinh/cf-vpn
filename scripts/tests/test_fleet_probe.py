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


def test_record_sent_writes_one_json_file_per_message(tmp_path):
    import json
    import os
    path = fp.record_sent(str(tmp_path / "q"), "-1003806233980", 4242, now=1_789_300_800)
    assert path == str(tmp_path / "q" / "-1003806233980_4242.json")
    assert json.load(open(path)) == {
        "chat_id": -1003806233980,
        "message_id": 4242,
        "source": "rwl_vpn_bot",
        "kind": "alert",
        "sent_at": 1_789_300_800,
    }
    assert oct(os.stat(path).st_mode & 0o777) == "0o600"
    assert not [p for p in os.listdir(tmp_path / "q") if p.endswith(".tmp")]
    # Disabled (empty dir) or no message id: nothing written, no error.
    assert fp.record_sent("", "-1", 1) is None
    assert fp.record_sent(str(tmp_path / "q"), "-1", None) is None


def test_configure_spool_reads_env():
    fp.configure_spool({"TELEGRAM_SPOOL_DIR": ""})
    assert fp.SPOOL == {"dir": ""}
    fp.configure_spool({"TELEGRAM_SPOOL_DIR": "/tmp/x"})
    assert fp.SPOOL["dir"] == "/tmp/x"
    fp.SPOOL["dir"] = "/var/lib/xiaoqie-janitor/spool"
