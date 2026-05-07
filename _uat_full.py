import json, subprocess, os, sys, time, urllib.request, urllib.error

results = []

WR_DIR = "C:/WorkSpace/agent/cli--agent-things/work-report-skill"

def run(cmd, timeout=10):
    r = subprocess.run(cmd, shell=True, capture_output=True, timeout=timeout)
    return r.stdout.decode('utf-8', errors='replace'), r.stderr.decode('utf-8', errors='replace'), r.returncode

def parse_jsonl(s):
    for line in s.strip().split('\n'):
        line = line.strip()
        if line:
            return json.loads(line)
    return None

# TC1: daemon_not_running error includes suggestion in message
print("=== TC1: daemon_not_running error includes suggestion ===")
run("taskkill /F /IM wr.exe 2>/dev/null", timeout=5)
time.sleep(2)
out, err, rc = run(f"cd {WR_DIR} && ./wr.exe list 2>/dev/null")
obj = parse_jsonl(out)
tc1 = False
if obj and obj.get("type") == "error" and obj.get("error_code") == "daemon_not_running":
    msg = obj.get("message", "")
    if msg and len(msg) > 0 and "wr agent daemon start" in msg.lower():
        tc1 = True
        print(f"  PASS: message contains suggestion")
    else:
        print(f"  FAIL: message missing or no suggestion: {msg}")
else:
    print(f"  FAIL: unexpected output: type={obj.get('type') if obj else None}, error_code={obj.get('error_code') if obj else None}")
results.append(("TC1", tc1))

# TC2: wr status when daemon is offline returns error
print("\n=== TC2: wr status offline ===")
out, err, rc = run(f"cd {WR_DIR} && ./wr.exe status 2>/dev/null")
obj = parse_jsonl(out)
tc2 = False
if obj:
    checks = []
    if obj.get("type") == "error" and obj.get("error_code") == "daemon_not_running":
        checks.append("type=error, error_code=daemon_not_running")
    else:
        checks.append(f"FAIL: type={obj.get('type')}, error_code={obj.get('error_code')}")
    msg = obj.get("message", "")
    if msg and "wr agent daemon start" in msg.lower():
        checks.append("message contains suggestion")
    else:
        checks.append(f"FAIL: message missing suggestion")
    print(f"  {'; '.join(checks)}")
    tc2 = all("FAIL" not in c for c in checks)
else:
    print(f"  FAIL: no JSONL output")
results.append(("TC2", tc2))

# TC3: wr status when daemon is running
print("\n=== TC3: wr status online ===")
run(f"cd {WR_DIR} && ./wr.exe agent daemon stop 2>/dev/null", timeout=10)
time.sleep(1)
run(f"cd {WR_DIR} && ./wr.exe agent daemon start --detach 2>/dev/null", timeout=15)
time.sleep(3)
out, err, rc = run(f"cd {WR_DIR} && ./wr.exe status 2>/dev/null")
obj = parse_jsonl(out)
tc3 = False
if obj:
    ds = obj.get("data", {}).get("daemon", {})
    checks = []
    if obj.get("type") == "result":
        checks.append("type=result")
    else:
        checks.append(f"FAIL: type={obj.get('type')}")
    if ds.get("status") == "running":
        checks.append("daemon.status=running")
    else:
        checks.append(f"FAIL: daemon.status={ds.get('status')}")
    if ds.get("port") and isinstance(ds.get("port"), int):
        checks.append(f"daemon.port={ds['port']}")
    else:
        checks.append(f"FAIL: daemon.port={ds.get('port')}")
    if ds.get("pid") and isinstance(ds.get("pid"), int):
        checks.append(f"daemon.pid={ds['pid']}")
    else:
        checks.append(f"FAIL: daemon.pid={ds.get('pid')}")
    if "config" in obj.get("data", {}):
        checks.append("config present")
    if rc == 0:
        checks.append("exit_code=0")
    else:
        checks.append(f"FAIL: exit_code={rc}")
    print(f"  {'; '.join(checks)}")
    tc3 = all("FAIL" not in c for c in checks)
else:
    print(f"  FAIL: no JSONL output")
results.append(("TC3", tc3))

# TC4: Config completeness accuracy (daemon running)
print("\n=== TC4: Config completeness accuracy ===")
out, err, rc = run(f"cd {WR_DIR} && ./wr.exe status 2>/dev/null")
obj = parse_jsonl(out)
tc4 = False
if obj and obj.get("type") == "result":
    cfg = obj.get("data", {}).get("config", {})
    checks = []
    po = cfg.get("pushover", {})
    if "configured" in po:
        checks.append(f"pushover.configured={po['configured']}")
    else:
        checks.append("FAIL: pushover.configured missing")
    llm = cfg.get("llm", {})
    text_cfg = llm.get("text", {})
    if "configured" in text_cfg:
        checks.append(f"llm.text.configured={text_cfg['configured']}")
    else:
        checks.append("FAIL: llm.text.configured missing")
    vision_cfg = llm.get("vision", {})
    if "configured" in vision_cfg:
        checks.append(f"llm.vision.configured={vision_cfg['configured']}")
    else:
        checks.append("FAIL: llm.vision.configured missing")
    print(f"  {'; '.join(checks)}")
    tc4 = all("FAIL" not in c for c in checks)
else:
    print(f"  FAIL: no result envelope (daemon not running?)")
results.append(("TC4", tc4))

# TC5: No secret leakage
print("\n=== TC5: No secret leakage ===")
out, err, rc = run(f"cd {WR_DIR} && ./wr.exe status 2>/dev/null")
config_path = os.path.expanduser("~/.work-report/config.json")
with open(config_path) as f:
    real_config = json.load(f)
secrets = []
po = real_config.get("pushover", {})
api_token = po.get("api_token", "")
user_key = po.get("user_key", "")
if api_token and api_token in out:
    secrets.append("pushover.api_token")
if user_key and user_key in out:
    secrets.append("pushover.user_key")
llm = real_config.get("llm", {})
for section_name in ["text", "vision"]:
    sec = llm.get(section_name, {})
    key = sec.get("api_key", "")
    if key and key in out:
        secrets.append(f"llm.{section_name}.api_key")
tc5 = len(secrets) == 0
if tc5:
    print("  PASS: no secrets in output")
else:
    print(f"  FAIL: secrets found: {secrets}")
results.append(("TC5", tc5))

# TC6: /api/status endpoint
print("\n=== TC6: /api/status endpoint ===")
out, err, rc = run(f"cd {WR_DIR} && ./wr.exe agent daemon stop 2>/dev/null", timeout=10)
time.sleep(1)
run(f"cd {WR_DIR} && ./wr.exe agent daemon start --detach 2>/dev/null", timeout=15)
time.sleep(3)
out, err, rc = run(f"cd {WR_DIR} && ./wr.exe status 2>/dev/null")
obj = parse_jsonl(out)
port = obj.get("data", {}).get("daemon", {}).get("port", 18080) if obj else 18080
tc6a = False
tc6b = False
checks = []
try:
    req = urllib.request.Request(f"http://localhost:{port}/api/status")
    resp = urllib.request.urlopen(req, timeout=5)
    body = json.loads(resp.read())
    if body.get("type") == "result" and "data" in body:
        d = body["data"]
        if "daemon" in d and "config" in d:
            tc6a = True
            checks.append("GET /api/status: valid response with daemon+config")
        else:
            checks.append("GET /api/status: missing fields")
    else:
        checks.append("GET /api/status: unexpected response")
except Exception as e:
    checks.append(f"GET /api/status: exception: {e}")
try:
    req = urllib.request.Request(f"http://localhost:{port}/api/status", data=b"", method="POST")
    resp = urllib.request.urlopen(req, timeout=5)
    body = json.loads(resp.read())
    checks.append("POST /api/status: unexpected success")
except urllib.error.HTTPError as e:
    body = json.loads(e.read())
    if body.get("type") == "error" and body.get("error_code") == "method_not_allowed":
        tc6b = True
        checks.append("POST /api/status: correctly rejected")
    else:
        checks.append(f"POST /api/status: wrong error: type={body.get('type')}, error_code={body.get('error_code')}")
except Exception as e:
    checks.append(f"POST /api/status: exception: {e}")
tc6 = tc6a and tc6b
print(f"  {'; '.join(checks)}")
results.append(("TC6", tc6))

# Cleanup
run("taskkill /F /IM wr.exe 2>/dev/null", timeout=5)

# Summary
print("\n" + "="*50)
print("SUMMARY")
print("="*50)
for tc, passed in results:
    print(f"  {tc}: {'PASS' if passed else 'FAIL'}")
all_pass = all(p for _, p in results)
if all_pass:
    print("\nOVERALL: ALL PASS")
else:
    failed = [tc for tc, p in results if not p]
    print(f"\nOVERALL: FAILED: {', '.join(failed)}")
