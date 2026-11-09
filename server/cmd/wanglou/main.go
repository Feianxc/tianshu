// Command wanglou is the hub: catalog, status, console, REST API and MCP endpoint
// in one binary.
//
//	wanglou serve                 运行服务
//	wanglou init                  创建首个管理员身份，打印一次性令牌
//	wanglou import <目录>         导入目录 YAML
//	wanglou export                把目录导出为 YAML 到标准输出
//	wanglou token create ...      创建身份和令牌
//	wanglou token list            列出身份
//	wanglou token revoke <id>     吊销身份
//	wanglou backup <目标文件>     写一份一致的数据库副本
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Feianxc/tianshu/server/internal/api"
	"github.com/Feianxc/tianshu/server/internal/catalog"
	"github.com/Feianxc/tianshu/server/internal/connector/github"
	"github.com/Feianxc/tianshu/server/internal/engine"
	"github.com/Feianxc/tianshu/server/internal/mcpsrv"
	"github.com/Feianxc/tianshu/server/internal/model"
	"github.com/Feianxc/tianshu/server/internal/oauth"
	"github.com/Feianxc/tianshu/server/internal/probe"
	"github.com/Feianxc/tianshu/server/internal/store"
	"github.com/Feianxc/tianshu/server/internal/view"
	"github.com/Feianxc/tianshu/server/internal/webui"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: wanglou serve|init|import|export|token|backup")
		os.Exit(2)
	}
	logger := log.New(os.Stderr, "", log.LstdFlags)
	st, err := store.Open(env("WANGLOU_DATA", "./data"))
	if err != nil {
		logger.Fatalf("打开数据库: %v", err)
	}
	defer st.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch os.Args[1] {
	case "serve":
		err = serve(ctx, st, logger)
	case "init":
		err = initAdmin(ctx, st)
	case "import":
		if len(os.Args) < 3 {
			logger.Fatal("用法: wanglou import <目录>")
		}
		err = importDir(ctx, st, os.Args[2])
	case "export":
		err = exportYAML(ctx, st)
	case "token":
		err = tokenCmd(ctx, st, os.Args[2:])
	case "backup":
		if len(os.Args) < 3 {
			logger.Fatal("用法: wanglou backup <目标文件>")
		}
		err = st.Backup(ctx, os.Args[2])
	default:
		logger.Fatalf("未知子命令 %s", os.Args[1])
	}
	if err != nil {
		logger.Fatal(err)
	}
}

func initAdmin(ctx context.Context, st *store.Store) error {
	list, err := st.ListPrincipals(ctx)
	if err != nil {
		return err
	}
	for _, p := range list {
		if p.Kind == "human" && p.RevokedAt == nil {
			return errors.New("已有管理员身份。需要新令牌时用 wanglou token create --kind human")
		}
	}
	p, tok, err := st.CreatePrincipal(ctx, model.Principal{Kind: "human", Name: "owner", Scopes: model.AllScopes, Workspaces: []string{"*"}, Devices: []string{"*"}})
	if err != nil {
		return err
	}
	fmt.Printf("已创建管理员 %s。令牌只显示这一次，请存入密匣：\n%s\n", p.Name, tok)
	return nil
}

func importDir(ctx context.Context, st *store.Store, dir string) error {
	f, err := catalog.Load(dir)
	if err != nil {
		return err
	}
	r, err := catalog.Import(ctx, st, f)
	if err != nil {
		return err
	}
	fmt.Printf("导入完成：空间 %d，新建 %d，更新 %d，关系 %d，探测 %d\n", r.Workspaces, r.Created, r.Updated, r.Relations, r.Checks)
	return nil
}

func exportYAML(ctx context.Context, st *store.Store) error {
	f, err := catalog.Export(ctx, st, model.System())
	if err != nil {
		return err
	}
	b, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(b)
	return err
}

func tokenCmd(ctx context.Context, st *store.Store, args []string) error {
	if len(args) == 0 {
		return errors.New("用法: wanglou token create|list|revoke")
	}
	switch args[0] {
	case "list":
		list, err := st.ListPrincipals(ctx)
		if err != nil {
			return err
		}
		for _, p := range list {
			state := "有效"
			if p.RevokedAt != nil {
				state = "已吊销"
			}
			fmt.Printf("%s\t%s\t%s\t%s\t%v\t%v\t%s\n", p.ID, p.Kind, p.Name, p.TokenPrefix, p.Scopes, p.Workspaces, state)
		}
		return nil
	case "revoke":
		if len(args) < 2 {
			return errors.New("用法: wanglou token revoke <id>")
		}
		return st.RevokePrincipal(ctx, args[1])
	case "create":
		fs := flag.NewFlagSet("token create", flag.ContinueOnError)
		kind := fs.String("kind", "agent", "human|agent|collector")
		name := fs.String("name", "", "名称")
		scopes := fs.String("scopes", "read", "逗号分隔")
		spaces := fs.String("workspaces", "", "逗号分隔的空间 slug，* 表示全部")
		devices := fs.String("devices", "", "collector 可上报的设备 slug，逗号分隔")
		ttl := fs.Int("ttl-days", 0, "有效天数，0 表示不过期")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		split := func(s string) []string {
			out := []string{}
			for _, x := range strings.Split(s, ",") {
				if x = strings.TrimSpace(x); x != "" {
					out = append(out, x)
				}
			}
			return out
		}
		p := model.Principal{Kind: *kind, Name: *name, Scopes: split(*scopes), Workspaces: split(*spaces), Devices: split(*devices)}
		if *ttl > 0 {
			t := st.Now().Add(time.Duration(*ttl) * 24 * time.Hour)
			p.ExpiresAt = &t
		}
		_, tok, err := st.CreatePrincipal(ctx, p)
		if err != nil {
			return err
		}
		fmt.Println(tok)
		return nil
	}
	return errors.New("未知子命令")
}

// githubToken reads a token from the environment, or borrows the gh CLI's login
// for local use. It stays in memory and is never written to the database.
func githubToken() func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		if t := os.Getenv("WANGLOU_GITHUB_TOKEN"); t != "" {
			return t, nil
		}
		out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}
}

func serve(ctx context.Context, st *store.Store, logger *log.Logger) error {
	addr := env("WANGLOU_ADDR", "127.0.0.1:8787")
	public := strings.TrimRight(os.Getenv("WANGLOU_PUBLIC_URL"), "/")
	trustCF := os.Getenv("WANGLOU_TRUST_CF") == "1"
	vw := &view.Service{Store: st}
	// OAuth exists for clients on the public internet. Without a public address
	// there is nothing to advertise, and agent tokens still work.
	var as *oauth.Server
	resource, metadata := "", ""
	if public != "" {
		as = &oauth.Server{Store: st, Public: public, MetadataHosts: oauth.DefaultMetadataHosts}
		resource, metadata = as.Resource(), as.MetadataURL()
	}
	srv := api.New(api.Config{Store: st, View: vw, MCP: mcpsrv.Handler(st, vw, resource, metadata, trustCF), OAuth: as, Web: webui.Handler(),
		TrustCF: trustCF, Secure: strings.HasPrefix(public, "https://"), Log: logger})
	eng := &engine.Engine{Store: st, View: vw, Log: logger,
		Probe: &probe.Scheduler{Store: st, Vantage: env("WANGLOU_VANTAGE", "hub")}}
	if os.Getenv("WANGLOU_GITHUB") != "off" {
		eng.GitHub = &github.Connector{Store: st, Token: githubToken(), Workspace: env("WANGLOU_GITHUB_WORKSPACE", "personal"), Interval: 15 * time.Minute}
	}
	go eng.Run(ctx)
	hs := &http.Server{Addr: addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(c)
	}()
	logger.Printf("天枢监听 http://%s", addr)
	if err := hs.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
