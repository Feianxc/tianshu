package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Feianxc/tianshu/server/internal/model"
)

// Journal collection reads, never follows: each round asks the journal for what was
// logged at warning level or worse in a short window, plus the last lines of every
// failed unit, since a service that dies usually says why at info level. The window
// overlaps the previous round on purpose; the hub drops lines it already has.

const (
	maxLogLines   = 120
	maxLogMessage = 600
	failedUnitMax = 5
	failedTail    = "12"
)

// journalFields keeps the output small: only what the report carries.
const journalFields = "PRIORITY,_SYSTEMD_UNIT,UNIT,SYSLOG_IDENTIFIER,MESSAGE"

// noisyIdent are programs whose warnings are other people's behaviour, not this
// host's faults. An exposed sshd logs an error for every scanner that hangs up, and
// that flood would bury the one line that matters. Login pressure is already counted
// by the SSH posture signal.
var noisyIdent = map[string]bool{"sshd": true, "sshd-session": true}

// firewallDrop recognises a firewall logging a dropped packet. On an exposed host
// these arrive every few seconds from scanners and say nothing about the host.
func firewallDrop(msg string) bool {
	return strings.HasPrefix(msg, "[UFW BLOCK]") || strings.HasPrefix(msg, "[UFW AUDIT]") || strings.HasPrefix(msg, "[UFW LIMIT BLOCK]")
}

// logWindow is how far back one round looks: twice the interval, at least three minutes.
func logWindow(intervalS int64) int {
	return int(max(intervalS*2, 180))
}

type journalEntry struct {
	TS       string `json:"__REALTIME_TIMESTAMP"`
	Priority string `json:"PRIORITY"`
	Unit     string `json:"_SYSTEMD_UNIT"`
	ObjUnit  string `json:"UNIT"` // set by systemd itself when it reports on a unit
	Ident    string `json:"SYSLOG_IDENTIFIER"`
	Message  any    `json:"MESSAGE"` // a string, or a byte array when not valid UTF-8
}

// parseJournal reads `journalctl -o json` lines. keepAll lifts the noise filter, for
// the lines of a failed unit, where every line is wanted.
func parseJournal(s string, keepAll bool) []model.ReportLog {
	out := []model.ReportLog{}
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 256<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] != '{' {
			continue
		}
		var e journalEntry
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		us, err := strconv.ParseInt(e.TS, 10, 64)
		if err != nil || us <= 0 {
			continue
		}
		msg := ""
		switch m := e.Message.(type) {
		case string:
			msg = m
		case []any:
			b := make([]byte, 0, len(m))
			for _, x := range m {
				if f, ok := x.(float64); ok {
					b = append(b, byte(f))
				}
			}
			msg = strings.ToValidUTF8(string(b), "?")
		}
		msg = strings.TrimSpace(msg)
		if msg == "" || (!keepAll && (noisyIdent[e.Ident] || firewallDrop(msg))) {
			continue
		}
		if r := []rune(msg); len(r) > maxLogMessage {
			msg = string(r[:maxLogMessage])
		}
		pri, err := strconv.Atoi(e.Priority)
		if err != nil || pri < 0 || pri > 7 {
			pri = 6
		}
		unit := e.ObjUnit
		if unit == "" {
			unit = e.Unit
		}
		out = append(out, model.ReportLog{TS: time.UnixMicro(us).UTC(), Unit: unit, Ident: e.Ident, Priority: pri, Message: msg})
	}
	return out
}

// foldLogs merges identical lines from one unit into the newest one and keeps the
// most recent lines when there are too many. A crash loop repeats itself; saying it
// once with a count leaves room for everything else.
func foldLogs(in []model.ReportLog) []model.ReportLog {
	type key struct{ unit, msg string }
	at := map[key]int{}
	out := make([]model.ReportLog, 0, len(in))
	for _, l := range in {
		k := key{l.Unit, l.Message}
		if i, ok := at[k]; ok {
			if l.TS.Equal(out[i].TS) {
				continue // the same line, read by both queries
			}
			out[i].Repeat++
			if l.TS.After(out[i].TS) {
				out[i].TS = l.TS
			}
			if l.Priority < out[i].Priority {
				out[i].Priority = l.Priority
			}
			continue
		}
		at[k] = len(out)
		out = append(out, l)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TS.Before(out[j].TS) })
	if len(out) > maxLogLines {
		out = out[len(out)-maxLogLines:]
	}
	return out
}

// collectJournal is the local Linux path. A collector that may not read the journal
// (no systemd-journal group) gets an empty answer from journalctl, which is reported
// as it is: no lines, and a note so the hub can say logs are not being collected.
func collectJournal(ctx context.Context, r *model.Report, intervalS int64) {
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	base := []string{"--no-pager", "-o", "json", "--output-fields=" + journalFields}
	run := func(args ...string) (string, error) {
		out, err := exec.CommandContext(c, "journalctl", append(base, args...)...).Output()
		return string(out), err
	}
	recent, err := run("-p", "warning", "--since", "-"+strconv.Itoa(logWindow(intervalS))+"s", "-n", "150")
	if err != nil {
		r.Errors = append(r.Errors, "journal: "+err.Error())
		return
	}
	logs := parseJournal(recent, false)
	failed := 0
	for _, u := range r.Units {
		if u.Active != "failed" || failed >= failedUnitMax {
			continue
		}
		failed++
		if tail, err := run("-u", u.Name, "-n", failedTail); err == nil {
			logs = append(logs, parseJournal(tail, true)...)
		}
	}
	r.Logs = foldLogs(logs)
}
