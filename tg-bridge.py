#!/usr/bin/env python3
"""Local Telegram getUpdates -> /telegram/webhook bridge (DEV/TEST ONLY).

Polls Telegram for the bot's incoming messages (outbound HTTPS, no public
inbound port) and forwards each update to the local backend webhook with the
shared-secret header the fork's TelegramWebhook expects. Lets the bot-OTP login
flow work end-to-end on localhost without exposing the backend via a tunnel.

Reads TELEGRAM_BOT_TOKEN + TELEGRAM_WEBHOOK_SECRET from the sibling .env.
"""
import json
import os
import time
import urllib.parse
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))


def load_env():
    env = {}
    with open(os.path.join(HERE, ".env")) as f:
        for line in f:
            line = line.strip()
            if "=" in line and not line.startswith("#"):
                k, v = line.split("=", 1)
                env[k] = v
    return env


env = load_env()
TOKEN = env["TELEGRAM_BOT_TOKEN"]
SECRET = env.get("TELEGRAM_WEBHOOK_SECRET", "")
API = "https://api.telegram.org/bot" + TOKEN
WEBHOOK = "http://localhost:8080/telegram/webhook"


def call(method, params=None):
    url = API + "/" + method
    data = urllib.parse.urlencode(params).encode() if params else None
    req = urllib.request.Request(url, data=data)
    with urllib.request.urlopen(req, timeout=40) as r:
        return json.load(r)


# getUpdates and a set webhook are mutually exclusive; ensure polling mode.
try:
    call("deleteWebhook", {"drop_pending_updates": "false"})
except Exception as e:
    print("[tg-bridge] deleteWebhook:", e, flush=True)

offset = 0
print("[tg-bridge] polling getUpdates -> " + WEBHOOK, flush=True)
while True:
    try:
        resp = call("getUpdates", {
            "offset": offset,
            "timeout": 25,
            "allowed_updates": json.dumps(["message"]),
        })
        for upd in resp.get("result", []):
            offset = upd["update_id"] + 1
            body = json.dumps(upd).encode()
            wreq = urllib.request.Request(
                WEBHOOK, data=body,
                headers={
                    "Content-Type": "application/json",
                    "X-Telegram-Bot-Api-Secret-Token": SECRET,
                },
            )
            try:
                with urllib.request.urlopen(wreq, timeout=15) as wr:
                    txt = (upd.get("message", {}) or {}).get("text", "")
                    print("[tg-bridge] fwd update %s (%r) -> %s" % (
                        upd["update_id"], txt, wr.status), flush=True)
            except Exception as e:
                print("[tg-bridge] forward error: %s" % e, flush=True)
    except Exception as e:
        print("[tg-bridge] poll error: %s" % e, flush=True)
        time.sleep(3)
