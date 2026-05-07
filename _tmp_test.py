import subprocess, time, os, json

# Clean up
subprocess.run(['taskkill', '/F', '/IM', 'wr.exe'], capture_output=True)
time.sleep(2)
try:
    os.remove(os.path.expanduser('~/.work-report/.daemon.json'))
except:
    pass

# Test 1: start --detach
r = subprocess.run(['./wr.exe', 'agent', 'daemon', 'start', '--detach'], capture_output=True, timeout=20)
print('detach stdout:', r.stdout.decode('utf-8', 'replace'))
print('detach stderr:', r.stderr.decode('utf-8', 'replace'))
print('detach exit:', r.returncode)

time.sleep(2)

# Check status
r2 = subprocess.run(['./wr.exe', 'status'], capture_output=True, timeout=10)
print('status stdout:', r2.stdout.decode('utf-8', 'replace')[:300])
print('status exit:', r2.returncode)

# Stop
subprocess.run(['./wr.exe', 'agent', 'daemon', 'stop'], capture_output=True, timeout=10)
