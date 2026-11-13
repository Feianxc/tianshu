package agent

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Feianxc/tianshu/server/internal/model"
)

// sshScript runs on the target and reads only: /proc, df, systemctl, ss, the effective
// sshd settings, key fingerprints and recent journal lines at warning level or worse. It writes nothing and needs
// nothing installed, which is what makes it safe to point at production. Its output is a line protocol so no JSON has to be built in shell.
const sshScript = `export LC_ALL=C
cpu(){ awk '/^cpu /{print $2+$3+$4+$5+$6+$7+$8+$9, $5+$6}' /proc/stat; }
net(){ awk -F'[: ]+' 'NR>2 && $2!="lo"{rx+=$3; tx+=$11} END{print rx+0, tx+0}' /proc/net/dev; }
c1=$(cpu); n1=$(net); sleep 1; c2=$(cpu); n2=$(net)
echo "CPU $c1 $c2"
echo "NET $n1 $n2"
echo "HOST $(hostname)"
echo "KERNEL $(uname -sr)"
echo "ARCH $(uname -m)"
(. /etc/os-release 2>/dev/null; echo "OS $PRETTY_NAME")
echo "UPTIME $(cut -d' ' -f1 /proc/uptime)"
echo "LOAD $(cut -d' ' -f1-3 /proc/loadavg)"
echo "NCPU $(nproc)"
awk '/^(MemTotal|MemAvailable|SwapTotal|SwapFree):/{print "MEM", $1, $2}' /proc/meminfo
df -P -B1 -x tmpfs -x devtmpfs -x overlay -x squashfs 2>/dev/null | awk 'NR>1{print "DISK", $6, $3, $2}'
echo "SYSTEMD $(systemctl is-system-running 2>/dev/null)"
systemctl list-units --type=service --all --no-legend --plain --no-pager 2>/dev/null | sed 's/^/UNIT /'
ss -lntH 2>/dev/null | sed 's/^/PORT /'
if [ "$(id -u)" = 0 ]; then
  /usr/sbin/sshd -T 2>/dev/null | awk '$1=="passwordauthentication"||$1=="permitrootlogin"||$1=="port"{print "SSHD",$1,$2}'
  echo "SSHFAIL $(journalctl -u ssh -u sshd --since -1h --no-pager -o cat 2>/dev/null | grep -cE 'Failed password|Invalid user')"
fi
ssh-keygen -lf "$HOME/.ssh/authorized_keys" 2>/dev/null | awk '{print "AKEY",$2}'
echo "F2B $(systemctl is-active fail2ban 2>/dev/null)"
J="journalctl --no-pager -o json --output-fields=PRIORITY,_SYSTEMD_UNIT,UNIT,SYSLOG_IDENTIFIER,MESSAGE"
$J -p warning --since "-${WIN:-180}s" -n 150 2>/dev/null | sed 's/^/LOGJ /'
for u in $(systemctl list-units --type=service --state=failed --no-legend --plain --no-pager 2>/dev/null | awk '{print $1}' | head -5); do
  $J -u "$u" -n 12 2>/dev/null | sed 's/^/LOGF /'
done
echo END
`

// CollectSSH reads one host over SSH. The binary and host alias are the user's own
// configuration; this package never sees a key.
func CollectSSH(ctx context.Context, binary, host string, intervalS int64) (model.Report, error) {
	if binary == "" {
		binary = "ssh"
	}
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(c, binary, "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", host, "bash", "-s")
	cmd.Stdin = strings.NewReader("WIN=" + strconv.Itoa(logWindow(intervalS)) + "\n" + sshScript)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	obs := time.Now().UTC()
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return model.Report{}, fmt.Errorf("%w: %s", err, msg)
	}
	return parseSSH(out.String(), obs)
}

func atof(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// parseSSH turns the line protocol into a report. A report without the END marker
// was cut short and is rejected whole; half a host must not pass as all of it.
func parseSSH(s string, obs time.Time) (model.Report, error) {
	r := model.Report{ObservedAt: obs, Host: &model.ReportHost{}}
	m := &model.ReportMetric{}
	r.Metrics = m
	var memTotal, memAvail, swapTotal, swapFree float64
	var unitLines, portLines, logLines, failedLines strings.Builder
	login := model.ReportSSH{}
	complete := false
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		key, rest, _ := strings.Cut(sc.Text(), " ")
		fields := strings.Fields(rest)
		switch key {
		case "CPU":
			if len(fields) == 4 {
				total, idle := atof(fields[2])-atof(fields[0]), atof(fields[3])-atof(fields[1])
				if total > 0 {
					m.CPUPct = f((total - idle) / total * 100)
				}
			}
		case "NET":
			if len(fields) == 4 {
				if rx, tx := atof(fields[2])-atof(fields[0]), atof(fields[3])-atof(fields[1]); rx >= 0 && tx >= 0 {
					m.NetRxBps, m.NetTxBps = f(rx), f(tx)
				}
			}
		case "HOST":
			r.Host.Hostname = rest
		case "KERNEL":
			r.Host.Kernel = rest
		case "ARCH":
			r.Host.Arch = rest
		case "OS":
			r.Host.OS = rest
		case "UPTIME":
			r.Host.UptimeS = int64(atof(rest))
		case "NCPU":
			r.Host.CPUCount = int(atof(rest))
		case "LOAD":
			if len(fields) == 3 {
				m.Load1, m.Load5, m.Load15 = f(atof(fields[0])), f(atof(fields[1])), f(atof(fields[2]))
			}
		case "MEM":
			if len(fields) == 2 {
				kb := atof(fields[1]) * 1024
				switch fields[0] {
				case "MemTotal:":
					memTotal = kb
				case "MemAvailable:":
					memAvail = kb
				case "SwapTotal:":
					swapTotal = kb
				case "SwapFree:":
					swapFree = kb
				}
			}
		case "DISK":
			if len(fields) == 3 && atof(fields[2]) > 0 {
				r.Disks = append(r.Disks, model.ReportDisk{Mount: fields[0], Used: atof(fields[1]), Total: atof(fields[2])})
			}
		case "SYSTEMD":
			r.SystemdState = rest
		case "UNIT":
			unitLines.WriteString(rest + "\n")
		case "PORT":
			portLines.WriteString(rest + "\n")
		case "SSHD":
			if len(fields) == 2 {
				switch fields[0] {
				case "passwordauthentication":
					login.PasswordAuth = fields[1]
				case "permitrootlogin":
					login.RootLogin = fields[1]
				case "port":
					login.Ports = append(login.Ports, int(atof(fields[1])))
				}
			}
		case "SSHFAIL":
			if n, err := strconv.Atoi(rest); err == nil {
				login.FailedLastHour = &n
			}
		case "AKEY":
			if len(fields) == 1 {
				login.Keys = append(login.Keys, fields[0])
			}
		case "F2B":
			login.Fail2ban = rest
		case "LOGJ":
			logLines.WriteString(rest + "\n")
		case "LOGF":
			failedLines.WriteString(rest + "\n")
		case "END":
			complete = true
		}
	}
	if err := sc.Err(); err != nil {
		return r, err
	}
	if !complete {
		return r, errors.New("采集输出不完整")
	}
	if memTotal > 0 {
		m.MemUsed, m.MemTotal = f(memTotal-memAvail), f(memTotal)
	}
	if swapTotal > 0 {
		m.SwapUsed, m.SwapTotal = f(swapTotal-swapFree), f(swapTotal)
	}
	r.Units = parseUnits(unitLines.String())
	r.Ports = parsePorts(portLines.String())
	r.Logs = foldLogs(append(parseJournal(logLines.String(), false), parseJournal(failedLines.String(), true)...))
	if login.PasswordAuth != "" || len(login.Keys) > 0 {
		r.SSH = &login
	}
	return r, nil
}
