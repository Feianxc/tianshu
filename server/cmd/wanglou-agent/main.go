// Command wanglou-agent collects status and reports it to the hub.
//
//	wanglou-agent run -config agent.yaml    持续采集
//	wanglou-agent once -config agent.yaml   采集一次并打印，不上报，用于检查
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Feianxc/tianshu/server/internal/agent"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	cfgPath := fs.String("config", "agent.yaml", "配置文件")
	_ = fs.Parse(os.Args[2:])
	logger := log.New(os.Stderr, "", log.LstdFlags)
	cfg, err := agent.LoadConfig(*cfgPath)
	if err != nil {
		logger.Fatalf("读取配置: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch os.Args[1] {
	case "run":
		tok, err := cfg.Token()
		if err != nil {
			logger.Fatal(err)
		}
		cl := &agent.Client{Hub: cfg.Hub, Token: tok, HTTP: &http.Client{Timeout: 20 * time.Second}}
		logger.Printf("wanglou-agent %s 上报到 %s", agent.Version, cfg.Hub)
		agent.Run(ctx, cfg, cl, logger)
	case "once":
		out := map[string]any{}
		if len(cfg.GitRoots) > 0 {
			repos, errs := agent.ScanRepos(ctx, cfg.GitRoots, cfg.GitMaxDepth)
			out["repos"], out["errors"] = repos, errs
		}
		for _, t := range cfg.SSH {
			r, err := agent.CollectSSH(ctx, cfg.SSHBinary, t.Host, t.IntervalS)
			if err != nil {
				out[t.Device] = err.Error()
			} else {
				out[t.Device] = r
			}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "用法: wanglou-agent run|once -config agent.yaml")
	os.Exit(2)
}
