import json, subprocess, os, sys, time, urllib.request, urllib.error

results = []

def run(cmd, timeout=10):
    r = subprocess.run(cmd, shell=True, capture_output=True, text=True, timeout=timeout)
    return r.stdout, r.stderr, r.returncode

def parse_jsonl(s):
    for line in s.strip().split('\n'):
        line = line.strip()
        if line:
            return json.loads(line)
    return None

# TC1: daemon_not_running error includes suggestion field
print("=== TC1: daemon_not_running error includes suggestion ===")
run("taskkill /F /IM wr.exe 2>/dev/null", timeout=5)
time.sleep(2)
out, err, rc = run("cd C:/WorkSpace/agent/work-report-skill && ./wr.exe list 2>/dev/null")
obj = parse_jsonl(out)
tc1 = False
if obj and obj.get("status") == "error" and obj.get("code") == "daemon_not_running":
    sug = obj.get("suggestion", "")
    if sug and len(sug) > 0 and "wr daemon start" in sug.lower():
        tc1 = True
        print(f"  PASS: suggestion='{sug}'")
    else:
        print(f"  FAIL: suggestion missing or empty: {sug}")
else:
    print(f"  FAIL: unexpected output: {out[:200]}")
results.append(("TC1", tc1))

# TC2: wr status when daemon is offline
print("\n=== TC2: wr status offline ===")
out, err, rc = run("cd C:/WorkSpace/agent/work-report-skill && ./wr.exe status 2>/dev/null")
obj = parse_jsonl(out)
tc2 = False
if obj:
    ds = obj.get("data", {}).get("daemon", {})
    cfg = obj.get("data", {}).get("config", {})
    checks = []
    if obj.get("status") == "success":
        checks.append("status=success")
    else:
        checks.append(f"FAIL: status={obj.get('status')}")
    if ds.get("status") == "not_running":
        checks.append("daemon.status=not_running")
    else:
        checks.append(f"FAIL: daemon.status={ds.get('status')}")
    if ds.get("suggestion") and len(ds.get("suggestion")) > 0:
        checks.append("daemon.suggestion present")
    else:
        checks.append("FAIL: daemon.suggestion missing")
    for section in ["pushover", "llm"]:
        if section in cfg:
            checks.append(f"config.{section} present")
    output_lower = out.lower()
    secrets_found = []
    if "p1o7zm" in output_lower:
        secrets_found.append("pushover key")
    if rc == 0:
        checks.append("exit_code=0")
    else:
        checks.append(f"FAIL: exit_code={rc}")
    print(f"  {'; '.join(checks)}")
    tc2 = all("FAIL" not in c for c in checks)
results.append(("TC2", tc2))

# TC3: wr status when daemon is running
print("\n=== TC3: wr status online ===")
run("cd C:/WorkSpace/agent/work-report-skill && start /B wr.exe daemon start > _uat_dlog.txt 2>&1", timeout=5)
time.sleep(5)
out, err, rc = run("cd C:/WorkSpace/agent/work-report-skill && ./wr.exe status 2>/dev/null")
obj = parse_jsonl(out)
tc3 = False
if obj:
    ds = obj.get("data", {}).get("daemon", {})
    cfg = obj.get("data", {}).get("config", {})
    checks = []
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
results.append(("TC3", tc3))

# TC4: Config completeness accuracy
print("\n=== TC4: Config completeness accuracy ===")
run("taskkill /F /IM wr.exe 2>/dev/null", timeout=5)
time.sleep(2)
out, err, rc = run("cd C:/WorkSpace/agent/work-report-skill && ./wr.exe status 2>/dev/null")
obj = parse_jsonl(out)
tc4 = False
if obj:
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
results.append(("TC4", tc4))

# TC5: No secret leakage
print("\n=== TC5: No secret leakage ===")
out, err, rc = run("cd C:/WorkSpace/agent/work-report-skill && ./wr.exe status 2>/dev/null")
config_path = os.path.expanduser("~/.work-report/config.json")
with open(config_path) as f:
    real_config = json.load(f)
secrets = []
po = real_config.get("pushover", {})
api_token = po.get("api_token", "")
user_key = po.get("user_key", "")
if api_token and api_token in out:
    secrets.append(f"pushover.api_token")
if user_key and user_key in out:
    secrets.append(f"pushover.user_key")
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
run("cd C:/WorkSpace/agent/work-report-skill && start /B wr.exe daemon start > _uat_dlog.txt 2>&1", timeout=5)
time.sleep(5)
out, err, rc = run("cd C:/WorkSpace/agent/work-report-skill && ./wr.exe status 2>/dev/null")
obj = parse_jsonl(out)
port = obj.get("data", {}).get("daemon", {}).get("port", 18080) if obj else 18080
tc6a = False
tc6b = False
checks = []
try:
    req = urllib.request.Request(f"http://localhost:{port}/api/status")
    resp = urllib.request.urlopen(req, timeout=5)
    body = json.loads(resp.read())
    if body.get("status") == "success" and "data" in body:
        d = body["data"]
        if "daemon" in d and "config" in d:
            tc6a = True
            checks.append("GET /api/status: valid response with daemon+config")
        else:
            checks.append(f"GET /api/status: missing fields")
    else:
        checks.append(f"GET /api/status: unexpected response")
except Exception as e:
    checks.append(f"GET /api/status: exception: {e}")
try:
    req = urllib.request.Request(f"http://localhost:{port}/api/status", data=b"", method="POST")
    resp = urllib.request.urlopen(req, timeout=5)
    body = json.loads(resp.read())
    checks.append(f"POST /api/status: unexpected success")
except urllib.error.HTTPError as e:
    body = json.loads(e.read())
    if body.get("status") == "error" and "not allowed" in body.get("message", "").lower():
        tc6b = True
        checks.append("POST /api/status: correctly rejected")
    else:
        checks.append(f"POST /api/status: wrong error: {body}")
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
