import subprocess, time, os, json

# Clean up
subprocess.run(['taskkill', '/F', '/IM', 'wr.exe'], capture_output=True)
time.sleep(2)
try:
    os.remove(os.path.expanduser('~/.work-report/.daemon.json'))
except:
    pass

# Use Popen without --detach (daemon runs in foreground in this process)
proc = subprocess.Popen(
    ['./wr.exe', 'agent', 'daemon', 'start'],
    stdout=subprocess.PIPE, stderr=subprocess.PIPE,
    creationflags=subprocess.CREATE_NEW_PROCESS_GROUP if os.name == 'nt' else 0
)
time.sleep(4)

# Check status
r2 = subprocess.run(['./wr.exe', 'status'], capture_output=True, timeout=10)
out = r2.stdout.decode('utf-8', 'replace')
print('status:', out[:300])
print('exit:', r2.returncode)

# Parse
for line in out.strip().split('\n'):
    if line.strip():
        obj = json.loads(line.strip())
        print('type:', obj.get('type'), 'error_code:', obj.get('error_code'))

# Cleanup
subprocess.run(['./wr.exe', 'agent', 'daemon', 'stop'], capture_output=True, timeout=10)
proc.terminate()
