package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Feianxc/tianshu/server/internal/model"
)

func TestRunIDsAreStoredOnlyAsHashes(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	ws, err := st.UpsertWorkspace(ctx, model.Workspace{Slug: "p", Name: "p", Kind: "personal", CloudPolicy: "allow"})
	if err != nil {
		t.Fatal(err)
	}
	dev, err := st.CreateEntity(ctx, model.Entity{Kind: model.KindDevice, WorkspaceID: ws, Slug: "d", Name: "d"})
	if err != nil {
		t.Fatal(err)
	}
	secret := "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	now := time.Now().UTC()
	for i, run := range []string{secret, "wl_h_" + strings.Repeat("a", 40)} {
		r := model.Report{Device: "d", Collector: "agent", RunID: run, Seq: 1, SentAt: now, ObservedAt: now, IntervalS: 30}
		stats, err := st.Ingest(ctx, dev, r)
		if err != nil {
			t.Fatal(err)
		}
		if stats["dropped"] != 0 {
			t.Fatalf("第 %d 次不该被丢弃", i)
		}
	}
	var run, past string
	if err := st.r.QueryRow("SELECT run_id,past_runs FROM report_cursors").Scan(&run, &past); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(run+past, "ghp_") || strings.Contains(run+past, "wl_h_") {
		t.Fatalf("run_id 原文进了数据库: %s %s", run, past)
	}
	// 哈希后身份判断不变：旧运行的重放仍被挡住。
	stats, err := st.Ingest(ctx, dev, model.Report{Device: "d", Collector: "agent", RunID: secret, Seq: 2, SentAt: now, ObservedAt: now, IntervalS: 30})
	if err != nil {
		t.Fatal(err)
	}
	if stats["dropped"] != 1 {
		t.Fatal("旧运行的报告应被丢弃")
	}
}
