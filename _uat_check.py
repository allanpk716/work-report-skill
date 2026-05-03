import json, sys, subprocess

def tc1():
    result = subprocess.run(['./wr.exe', 'list'], capture_output=True, text=True, timeout=10)
    assert result.returncode == 1, f"exit={result.returncode}"
    obj = json.loads(result.stdout.strip().split('\n')[-1])
    assert obj['status'] == 'error'
    assert obj['code'] == 'daemon_not_running'
    assert 'suggestion' in obj and len(obj['suggestion']) > 0
    assert 'wr daemon start' in obj['suggestion']
    print("TC1 PASS")

def tc2():
    result = subprocess.run(['./wr.exe', 'status'], capture_output=True, text=True, timeout=10)
    lines = [json.loads(l) for l in result.stdout.strip().split('\n') if l.strip()]
    s = [l for l in lines if l.get('status') == 'success']
    assert len(s) > 0, "no success line"
    d = s[-1]['data']
    assert d['daemon']['status'] == 'not_running'
    assert len(d['daemon']['suggestion']) > 0
    c = d['config']
    assert 'configured' in c['pushover']
    assert 'configured' in c['llm']['text']
    assert 'configured' in c['llm']['vision']
    print("TC2 PASS")

def tc3():
    # Start daemon
    subprocess.Popen(['./wr.exe', 'daemon', 'start'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    import time; time.sleep(2)
    result = subprocess.run(['./wr.exe', 'status'], capture_output=True, text=True, timeout=10)
    lines = [json.loads(l) for l in result.stdout.strip().split('\n') if l.strip()]
    s = [l for l in lines if l.get('status') == 'success']
    assert len(s) > 0
    d = s[-1]['data']
    assert d['daemon']['status'] == 'running', f"status={d['daemon']['status']}"
    assert 'port' in d['daemon']
    assert 'pid' in d['daemon']
    c = d['config']
    assert 'configured' in c['pushover']
    print("TC3 PASS")
    # Stop daemon
    subprocess.run(['./wr.exe', 'daemon', 'stop'], capture_output=True, text=True, timeout=10)

def tc4():
    # Create a temp config with only pushover configured
    import os, shutil
    config_dir = os.path.expanduser('~/.work-report')
    config_path = os.path.join(config_dir, 'config.json')
    if os.path.exists(config_path):
        shutil.copy(config_path, config_path + '.bak')
    with open(config_path, 'w') as f:
        json.dump({'pushover': {'api_token': 'test_token', 'user_key': 'test_key'}}, f)
    result = subprocess.run(['./wr.exe', 'status'], capture_output=True, text=True, timeout=10)
    lines = [json.loads(l) for l in result.stdout.strip().split('\n') if l.strip()]
    s = [l for l in lines if l.get('status') == 'success']
    d = s[-1]['data']
    assert d['config']['pushover']['configured'] == True, f"pushover={d['config']['pushover']}"
    assert d['config']['llm']['text']['configured'] == False
    assert d['config']['llm']['vision']['configured'] == False
    print("TC4 PASS")
    # Restore
    if os.path.exists(config_path + '.bak'):
        shutil.copy(config_path + '.bak', config_path)
        os.remove(config_path + '.bak')

def tc5():
    # Configure all sections with real credentials
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
    result = subprocess.run(['./wr.exe', 'status'], capture_output=True, text=True, timeout=10)
    output = result.stdout + result.stderr
    assert 'secret_push_token' not in output, "pushover token leaked"
    assert 'secret_user_key' not in output, "pushover user_key leaked"
    assert 'secret_text_key' not in output, "text api_key leaked"
    assert 'secret_vision_key' not in output, "vision api_key leaked"
    print("TC5 PASS")

def tc6():
    import time
    # Start daemon
    subprocess.Popen(['./wr.exe', 'daemon', 'start'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    time.sleep(2)
    # Get port from status
    result = subprocess.run(['./wr.exe', 'status'], capture_output=True, text=True, timeout=10)
    lines = [json.loads(l) for l in result.stdout.strip().split('\n') if l.strip()]
    s = [l for l in lines if l.get('status') == 'success']
    port = s[-1]['data']['daemon']['port']
    # Test /api/status
    import urllib.request
    req = urllib.request.Request(f'http://localhost:{port}/api/status')
    with urllib.request.urlopen(req) as resp:
        data = json.loads(resp.read().decode())
    assert data['status'] == 'success'
    print("TC6a PASS")
    # Test wrong method
    req2 = urllib.request.Request(f'http://localhost:{port}/api/status', method='POST')
    try:
        with urllib.request.urlopen(req2) as resp:
            data2 = json.loads(resp.read().decode())
            assert data2.get('code') == 'method_not_allowed', f"code={data2.get('code')}"
    except urllib.error.HTTPError as e:
        body = json.loads(e.read().decode())
        assert body.get('code') == 'method_not_allowed', f"code={body.get('code')}"
    print("TC6b PASS")
    # Stop daemon
    subprocess.run(['./wr.exe', 'daemon', 'stop'], capture_output=True, text=True, timeout=10)

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
    if failed:
        print(f"\nFAILED: {', '.join(failed)}")
        sys.exit(1)
    else:
        print("\nALL PASSED")
