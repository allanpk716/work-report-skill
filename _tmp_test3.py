import subprocess, time, os, json

# Clean
subprocess.run(['taskkill', '/F', '/IM', 'wr.exe'], capture_output=True)
time.sleep(2)
try: os.remove(os.path.expanduser('~/.work-report/.daemon.json'))
except: pass

# Start daemon with Popen, capture ALL output
proc = subprocess.Popen(
    ['./wr.exe', 'agent', 'daemon', 'start'],
    stdout=subprocess.PIPE, stderr=subprocess.PIPE
)

# Read output with timeout
import threading
stdout_lines = []
stderr_lines = []

def read_stdout():
    for line in proc.stdout:
        stdout_lines.append(line.decode('utf-8', errors='replace'))

def read_stderr():
    for line in proc.stderr:
        stderr_lines.append(line.decode('utf-8', errors='replace'))

t1 = threading.Thread(target=read_stdout, daemon=True)
t2 = threading.Thread(target=read_stderr, daemon=True)
t1.start()
t2.start()

# Wait up to 15 seconds
time.sleep(15)

print(f"Process alive: {proc.poll() is None}")
print(f"Return code: {proc.poll()}")
print(f"Stdout lines: {len(stdout_lines)}")
for l in stdout_lines[:10]:
    print(f"  {l[:200]}")
print(f"Stderr lines: {len(stderr_lines)}")
for l in stderr_lines[:10]:
    print(f"  {l[:200]}")

# Check if daemon is running
r = subprocess.run(['./wr.exe', 'status'], capture_output=True, timeout=10)
print(f"Status: {r.stdout.decode('utf-8','replace')[:200]}")

proc.terminate()
