import subprocess, time, os

subprocess.run(['taskkill', '/F', '/IM', 'wr.exe'], capture_output=True)
time.sleep(2)
try: os.remove(os.path.expanduser('~/.work-report/.daemon.json'))
except: pass

proc = subprocess.Popen(['./wr.exe', 'agent', 'daemon', 'start'], stdout=subprocess.PIPE, stderr=subprocess.PIPE)

for i in range(30):
    time.sleep(1)
    r = subprocess.run(['./wr.exe', 'status'], capture_output=True, timeout=5)
    out = r.stdout.decode('utf-8', 'replace')
    if '"type":"result"' in out and '"running"' in out:
        print(f'OK after {i+1}s')
        break
    else:
        print(f'{i+1}s: {out[:80]}')
else:
    print('TIMEOUT after 30s')

proc.terminate()
