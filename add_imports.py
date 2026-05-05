data = open('C:/WorkSpace/agent/cli--agent-things/work-report-skill/cmd/agent_test.go', 'rb').read()

old = b'"testing"\r\n\r\n\tagentsdk "github.com/allan716/agent-cli-sdk"\r\n\r\n\t"wr/internal/config"\r\n)'
new = b'"testing"\r\n\t"time"\r\n\r\n\tagentsdk "github.com/allan716/agent-cli-sdk"\r\n\r\n\t"wr/internal/config"\r\n\t"wr/internal/daemon"\r\n)'

assert old in data, 'old not found'
data = data.replace(old, new, 1)

# Also add fmt, net, os/exec, runtime imports
old2 = b'\t"encoding/json"\r\n\t"errors"\r\n\t"os"\r\n\t"path/filepath"\r\n\t"strings"\r\n\t"testing"'
new2 = b'\t"encoding/json"\r\n\t"errors"\r\n\t"fmt"\r\n\t"net"\r\n\t"os"\r\n\t"os/exec"\r\n\t"path/filepath"\r\n\t"runtime"\r\n\t"strings"\r\n\t"testing"'

assert old2 in data, 'old2 not found'
data = data.replace(old2, new2, 1)

open('C:/WorkSpace/agent/cli--agent-things/work-report-skill/cmd/agent_test.go', 'wb').write(data)
print('Imports added, preserving CRLF')
