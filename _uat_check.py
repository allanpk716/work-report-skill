import json, sys, subprocess, time

def run_wr(args, timeout=10):
    result = subprocess.run(['./wr.exe'] + args, capture_output=True, timeout=timeout)
    stdout = result.stdout.decode('utf-8', errors='replace')
    stderr = result.stderr.decode('utf-8', errors='replace')
    return stdout, stderr, result.returncode

def parse_jsonl(text):
    for line in text.strip().split('\n'):
        line = line.strip()
        if line:
            return json.loads(line)
    return None

def stop_daemon():
    """Best-effort stop: try graceful stop, then force kill."""
    run_wr(['agent', 'daemon', 'stop'], timeout=5)
    time.sleep(1)
    subprocess.run(['taskkill', '/F', '/IM', 'wr.exe'], capture_output=True)
    time.sleep(1)

def start_daemon():
    """Start daemon using Popen (foreground mode) and wait for it to be ready."""
    proc = subprocess.Popen(
        ['./wr.exe', 'agent', 'daemon', 'start'],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        creationflags=0 if not hasattr(subprocess, 'CREATE_NEW_PROCESS_GROUP') else subprocess.CREATE_NEW_PROCESS_GROUP
    )
    # Poll until status returns a result envelope (up to 15s)
    for _ in range(30):
        time.sleep(0.5)
        out, _, _ = run_wr(['status'])
        obj = parse_jsonl(out)
        if obj and obj.get('type') == 'result':
            return proc, obj
    return proc, None

def tc1():
    out, err, rc = run_wr(['list'])
    assert rc != 0, f"exit={rc}"
    obj = parse_jsonl(out)
    assert obj is not None, f"no JSONL output: {out[:200]}"
    assert obj['type'] == 'error', f"type={obj.get('type')}"
    assert obj['error_code'] == 'daemon_not_running', f"error_code={obj.get('error_code')}"
    assert 'message' in obj and len(obj['message']) > 0, f"message={obj.get('message')}"
    assert 'wr agent daemon start' in obj['message'], f"suggestion missing agent prefix: {obj.get('message')}"
    print("TC1 PASS")

def tc2():
    out, err, rc = run_wr(['status'])
    lines = [json.loads(l) for l in out.strip().split('\n') if l.strip()]
    e = [l for l in lines if l.get('type') == 'error']
    assert len(e) > 0, "no error line"
    obj = e[-1]
    assert obj['error_code'] == 'daemon_not_running', f"error_code={obj.get('error_code')}"
    assert 'wr agent daemon start' in obj.get('message', ''), f"suggestion missing agent prefix"
    print("TC2 PASS")

def tc3():
    stop_daemon()
    proc, status_obj = start_daemon()
    assert status_obj is not None, "daemon failed to start"
    d = status_obj['data']
    assert d['daemon']['status'] == 'running', f"status={d['daemon']['status']}"
    assert 'port' in d['daemon']
    assert 'pid' in d['daemon']
    c = d['config']
    assert 'configured' in c['pushover']
    print("TC3 PASS")
    proc.terminate()

def tc4():
    import os, shutil
    # Daemon should have been stopped by TC3's terminate()
    time.sleep(1)
    config_dir = os.path.expanduser('~/.work-report')
    config_path = os.path.join(config_dir, 'config.json')
    if os.path.exists(config_path):
        shutil.copy(config_path, config_path + '.bak')
    with open(config_path, 'w') as f:
        json.dump({'pushover': {'api_token': 'test_token', 'user_key': 'test_key'}}, f)
    out, err, rc = run_wr(['status'])
    lines = [json.loads(l) for l in out.strip().split('\n') if l.strip()]
    s = [l for l in lines if l.get('type') == 'error']
    assert len(s) > 0, "daemon should be offline, expected error"
    print("TC4 PASS")
    # Restore
    if os.path.exists(config_path + '.bak'):
        shutil.copy(config_path + '.bak', config_path)
        os.remove(config_path + '.bak')

def tc5():
    import os
    config_dir = os.path.expanduser('~/.work-report')
    config_path = os.path.join(config_dir, 'config.json')
    with open(config_path, 'w') as f:
        json.dump({
            'pushover': {'api_token': 'secret_push_token', 'user_key': 'secret_user_key'},
            'llm': {
                'text': {'api_key': 'secret_text_key', 'base_url': 'http://localhost'},
                'vision': {'api_key': 'secret_vision_key', 'base_url': 'http://localhost'}
            }
        }, f)
    out, err, rc = run_wr(['status'])
    output = out + err
    assert 'secret_push_token' not in output, "pushover token leaked"
    assert 'secret_user_key' not in output, "pushover user_key leaked"
    assert 'secret_text_key' not in output, "text api_key leaked"
    assert 'secret_vision_key' not in output, "vision api_key leaked"
    print("TC5 PASS")

def tc6():
    import urllib.request, urllib.error
    stop_daemon()
    proc, status_obj = start_daemon()
    assert status_obj is not None, "daemon failed to start for TC6"
    port = status_obj['data']['daemon']['port']
    # Test /api/status
    req = urllib.request.Request(f'http://localhost:{port}/api/status')
    with urllib.request.urlopen(req) as resp:
        data = json.loads(resp.read().decode())
    assert data['type'] == 'result'
    print("TC6a PASS")
    # Test wrong method
    req2 = urllib.request.Request(f'http://localhost:{port}/api/status', method='POST')
    try:
        with urllib.request.urlopen(req2) as resp:
            data2 = json.loads(resp.read().decode())
            assert data2.get('error_code') == 'method_not_allowed', f"error_code={data2.get('error_code')}"
    except urllib.error.HTTPError as e:
        body = json.loads(e.read().decode())
        assert body.get('error_code') == 'method_not_allowed', f"error_code={body.get('error_code')}"
    print("TC6b PASS")
    proc.terminate()

if __name__ == '__main__':
    checks = {
        'TC1': tc1,
        'TC2': tc2,
        'TC3': tc3,
        'TC4': tc4,
        'TC5': tc5,
        'TC6': tc6,
    }
    failed = []
    for name, fn in checks.items():
        try:
            fn()
        except Exception as e:
            print(f"{name} FAIL: {e}")
            failed.append(name)
    # Final cleanup
    stop_daemon()
    if failed:
        print(f"\nFAILED: {', '.join(failed)}")
        sys.exit(1)
    else:
        print("\nALL PASSED")
