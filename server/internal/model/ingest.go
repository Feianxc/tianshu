package model

import "time"

// Report is what a collector sends. Optional numbers are pointers: an absent
// field means "not measured", which is different from zero.
type Report struct {
	Device       string        `json:"device"`
	Collector    string        `json:"collector"` // agent | ssh | termux | shortcut
	Version      string        `json:"version"`
	RunID        string        `json:"run_id,omitempty"` // random per collector start
	Seq          int64         `json:"seq,omitempty"`    // increases within a run
	SentAt       time.Time     `json:"sent_at,omitzero"` // collector clock at send
	IntervalS    int64         `json:"interval_s"`
	ObservedAt   time.Time     `json:"observed_at"`
	Host         *ReportHost   `json:"host,omitempty"`
	Metrics      *ReportMetric `json:"metrics,omitempty"`
	Disks        []ReportDisk  `json:"disks"`
	SystemdState string        `json:"systemd_state,omitempty"`
	Units        []ReportUnit  `json:"units"`
	Ports        []int         `json:"ports"`
	SSH          *ReportSSH    `json:"ssh,omitempty"`
	Logs         []ReportLog   `json:"logs,omitempty"`
	Repos        []ReportRepo  `json:"repos"`
	Errors       []string      `json:"errors"`
}

// ReportHost is slow-changing identity.
type ReportHost struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Kernel   string `json:"kernel"`
	Arch     string `json:"arch"`
	CPUCount int    `json:"cpu_count"`
	UptimeS  int64  `json:"uptime_s"`
}

// ReportMetric is one sample.
type ReportMetric struct {
	CPUPct     *float64 `json:"cpu_pct,omitempty"`
	MemUsed    *float64 `json:"mem_used,omitempty"`
	MemTotal   *float64 `json:"mem_total,omitempty"`
	SwapUsed   *float64 `json:"swap_used,omitempty"`
	SwapTotal  *float64 `json:"swap_total,omitempty"`
	Load1      *float64 `json:"load1,omitempty"`
	Load5      *float64 `json:"load5,omitempty"`
	Load15     *float64 `json:"load15,omitempty"`
	NetRxBps   *float64 `json:"net_rx_bps,omitempty"`
	NetTxBps   *float64 `json:"net_tx_bps,omitempty"`
	BatteryPct *float64 `json:"battery_pct,omitempty"`
	Charging   *bool    `json:"charging,omitempty"`
	Network    string   `json:"network,omitempty"`
}

// ReportDisk is one mounted filesystem.
type ReportDisk struct {
	Mount string  `json:"mount"`
	Used  float64 `json:"used"`
	Total float64 `json:"total"`
}

// ReportUnit is one service manager unit.
type ReportUnit struct {
	Name   string `json:"name"`
	Active string `json:"active"`
	Sub    string `json:"sub"`
}

// ReportLog is one journal line at warning level or worse, or one of the last lines
// of a failed unit. The collector cuts and de-duplicates; the hub redacts again.
type ReportLog struct {
	TS       time.Time `json:"ts"`
	Unit     string    `json:"unit,omitempty"`
	Ident    string    `json:"ident,omitempty"`
	Priority int       `json:"priority"` // syslog: 0 emergency … 3 error, 4 warning, 6 info
	Message  string    `json:"message"`
	Repeat   int       `json:"repeat,omitempty"` // identical lines folded into this one
}

// ReportSSH is how a host lets people in. None of it is secret: a fingerprint only
// identifies a public key. An empty field could not be read, usually because the
// collector is not root, and says nothing about the host.
type ReportSSH struct {
	PasswordAuth   string   `json:"password_auth,omitempty"`
	RootLogin      string   `json:"root_login,omitempty"`
	Ports          []int    `json:"ports,omitempty"`
	Fail2ban       string   `json:"fail2ban,omitempty"`
	FailedLastHour *int     `json:"failed_last_hour,omitempty"`
	Keys           []string `json:"keys,omitempty"`
}

// ReportRepo is one local working copy. Remote is sanitized by the collector
// and again by the hub.
type ReportRepo struct {
	Path         string     `json:"path"`
	Remote       string     `json:"remote"`
	Branch       string     `json:"branch"`
	Dirty        int        `json:"dirty"`
	Ahead        int        `json:"ahead"`
	Behind       int        `json:"behind"`
	Stash        int        `json:"stash"`
	HasUpstream  bool       `json:"has_upstream"`
	UserEmail    string     `json:"user_email"` // the identity new commits here will carry
	LastCommitAt *time.Time `json:"last_commit_at"`
	Error        string     `json:"error,omitempty"`
}
