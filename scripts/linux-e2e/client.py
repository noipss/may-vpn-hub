# Talks to mayvpnd over its socket like the app does: connect, wait, report, disconnect.
import json, socket, sys, time

def session(path):
    s = socket.socket(socket.AF_UNIX); s.connect(path); s.settimeout(30)
    f = s.makefile("rwb")
    def send(m):
        f.write((json.dumps(m) + "\n").encode()); f.flush()
    def until(pred, limit=25):
        end = time.time() + limit
        while time.time() < end:
            line = f.readline()
            if not line:
                break
            m = json.loads(line)
            if pred(m):
                return m
        raise SystemExit("timeout waiting: " + pred.__doc__ if pred.__doc__ else "timeout")
    return send, until

if __name__ == "__main__":
    path, op = sys.argv[1], sys.argv[2]
    send, until = session(path)
    if op == "connect":
        key = open(sys.argv[3]).read()
        ks = sys.argv[4] == "1"
        send({"id": 1, "op": "connect", "key": key, "prefs": {"killSwitch": ks}})
        r = until(lambda m: m.get("id") == 1)
        if not r.get("ok"):
            raise SystemExit("connect refused: " + r.get("error", ""))
        st = until(lambda m: m.get("ev") == "status" and m["status"]["state"] in ("connected", "idle"))
        if st["status"]["state"] != "connected":
            raise SystemExit("did not connect: " + st["status"].get("error", ""))
        stats = until(lambda m: m.get("ev") == "stats")
        print("CONNECTED ping=%sms rx=%s tx=%s" % (stats["stats"]["ping"], stats["stats"]["rx"], stats["stats"]["tx"]))
    elif op == "disconnect":
        send({"id": 2, "op": "disconnect"})
        until(lambda m: m.get("id") == 2)
        print("DISCONNECTED")
