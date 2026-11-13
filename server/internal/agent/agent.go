// Package agent is the collector. It only ever makes outbound calls: no listening
// port, no remote commands accepted, nothing for the hub to reach into. A collector
// that cannot be told to do anything is the point; control comes later, through a
// separate and much narrower path.
package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Feianxc/tianshu/server/internal/model"
)

// Version is reported with every report.
const Version = "0.2.0"

// Config is the collector's YAML configuration.
type Config struct {
	Hub         string   `yaml:"hub"`
	TokenFile   string   `yaml:"token_file"`
	Device      string   `yaml:"device"`
	IntervalS   int64    `yaml:"interval_s"`
	GitRoots    []string `yaml:"git_roots"`
	GitEveryS   int64    `yaml:"git_every_s"`
	GitMaxDepth int      `yaml:"git_max_depth"`
	SSH         []Target `yaml:"ssh"`
	SSHBinary   string   `yaml:"ssh_binary"`
}

// Target is a host read over SSH instead of running a collector on it.
type Target struct {
	Device    string `yaml:"device"`
	Host      string `yaml:"host"`
	IntervalS int64  `yaml:"interval_s"`
}

// LoadConfig reads and defaults a config file.
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := &Config{}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil {
		return nil, err
	}
	if c.Hub == "" {
		return nil, errors.New("缺少 hub")
	}
	if c.IntervalS <= 0 {
		c.IntervalS = 30
	}
	if c.GitEveryS <= 0 {
		c.GitEveryS = 600
	}
	if c.GitMaxDepth <= 0 {
		c.GitMaxDepth = 5
	}
	return c, nil
}

// Token returns the credential from the environment or the token file.
func (c *Config) Token() (string, error) {
	if t := os.Getenv("WANGLOU_TOKEN"); t != "" {
		return t, nil
	}
	if c.TokenFile == "" {
		return "", errors.New("没有令牌：设置 WANGLOU_TOKEN 或 token_file")
	}
	b, err := os.ReadFile(c.TokenFile)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// Client posts reports.
type Client struct {
	Hub   string
	Token string
	HTTP  *http.Client
	run   string
	seq   atomic.Int64
	once  sync.Once
}

// Send delivers one report.
func (c *Client) Send(ctx context.Context, r model.Report) error {
	// The hub orders reports by run and sequence, not by this machine's clock.
	c.once.Do(func() {
		b := make([]byte, 12)
		if _, err := rand.Read(b); err != nil {
			panic(err)
		}
		c.run = hex.EncodeToString(b)
	})
	r.RunID, r.Seq, r.SentAt = c.run, c.seq.Add(1), time.Now().UTC()
	if r.Disks == nil {
		r.Disks = []model.ReportDisk{}
	}
	if r.Units == nil {
		r.Units = []model.ReportUnit{}
	}
	if r.Ports == nil {
		r.Ports = []int{}
	}
	if r.Repos == nil {
		r.Repos = []model.ReportRepo{}
	}
	if r.Errors == nil {
		r.Errors = []string{}
	}
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.Hub, "/")+"/api/v1/ingest/report", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("hub 返回 %d：%s", resp.StatusCode, msg)
	}
	return nil
}

// Run collects and reports until ctx ends. Each source runs on its own schedule,
// so one unreachable SSH host delays nothing else. A failed send is dropped rather
// than queued: a late sample is worth less than the gap showing honestly.
func Run(ctx context.Context, c *Config, cl *Client, logger *log.Logger) {
	var wg sync.WaitGroup
	if c.Device != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runLocal(ctx, c, cl, logger)
		}()
	}
	for _, t := range c.SSH {
		wg.Add(1)
		go func(t Target) {
			defer wg.Done()
			runSSH(ctx, c, t, cl, logger)
		}(t)
	}
	wg.Wait()
}

func runLocal(ctx context.Context, c *Config, cl *Client, logger *log.Logger) {
	col := newLocal(c.IntervalS)
	interval := time.Duration(c.IntervalS) * time.Second
	var lastGit time.Time
	tick := func() {
		r := col.Collect(ctx)
		r.Device, r.Collector, r.Version, r.IntervalS = c.Device, "agent", Version, c.IntervalS
		if len(c.GitRoots) > 0 && time.Since(lastGit) >= time.Duration(c.GitEveryS)*time.Second {
			repos, errs := ScanRepos(ctx, c.GitRoots, c.GitMaxDepth)
			r.Repos = repos
			r.Errors = append(r.Errors, errs...)
			lastGit = time.Now()
		}
		if err := cl.Send(ctx, r); err != nil && ctx.Err() == nil {
			logger.Printf("上报失败 %s: %v", c.Device, err)
		}
	}
	tick()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick()
		}
	}
}

func runSSH(ctx context.Context, c *Config, tg Target, cl *Client, logger *log.Logger) {
	every := tg.IntervalS
	if every <= 0 {
		every = 60
	}
	tick := func() {
		r, err := CollectSSH(ctx, c.SSHBinary, tg.Host, every)
		if err != nil {
			// No report means the device's heartbeat simply ages, which is the
			// truth: from here, we do not know.
			logger.Printf("SSH 采集失败 %s: %v", tg.Device, err)
			return
		}
		r.Device, r.Collector, r.Version, r.IntervalS = tg.Device, "ssh", Version, every
		if err := cl.Send(ctx, r); err != nil && ctx.Err() == nil {
			logger.Printf("上报失败 %s: %v", tg.Device, err)
		}
	}
	tick()
	t := time.NewTicker(time.Duration(every) * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick()
		}
	}
}
