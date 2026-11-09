// Package engine runs the background loops. Each loop is independent, so one
// failing source never stops the others or the read path.
package engine

import (
	"context"
	"log"
	"time"

	"github.com/Feianxc/tianshu/server/internal/connector/github"
	"github.com/Feianxc/tianshu/server/internal/probe"
	"github.com/Feianxc/tianshu/server/internal/rules"
	"github.com/Feianxc/tianshu/server/internal/store"
	"github.com/Feianxc/tianshu/server/internal/view"
)

// Engine owns the schedulers.
type Engine struct {
	Store  *store.Store
	View   *view.Service
	Probe  *probe.Scheduler
	GitHub *github.Connector
	Log    *log.Logger
}

// Evaluate recomputes findings once.
func (e *Engine) Evaluate(ctx context.Context) error {
	snap, err := e.View.Snapshot(ctx)
	if err != nil {
		return err
	}
	has, err := e.Store.EntitiesWithChecks(ctx)
	if err != nil {
		return err
	}
	fs := rules.Evaluate(rules.Inputs{Snapshot: snap, HasCheck: has,
		DiskTrend: func(id string) (float64, time.Duration, bool) {
			perDay, span, ok, err := e.Store.DiskTrend(ctx, id, 14*24*time.Hour, 24*time.Hour)
			return perDay, span, ok && err == nil
		}})
	keep := make(map[string]bool, len(fs))
	for _, f := range fs {
		keep[f.Rule+"|"+f.EntityID] = true
		if err := e.Store.UpsertFinding(ctx, f); err != nil {
			return err
		}
	}
	_, err = e.Store.ResolveAbsent(ctx, keep)
	return err
}

func every(ctx context.Context, d time.Duration, f func()) {
	f()
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f()
		}
	}
}

// Run blocks until ctx ends.
func (e *Engine) Run(ctx context.Context) {
	logf := func(what string, err error) {
		if err != nil && ctx.Err() == nil && e.Log != nil {
			e.Log.Printf("%s: %v", what, err)
		}
	}
	logf("probe sources", e.Store.DropOtherProbeSources(ctx, "probe:"+e.Probe.Vantage))
	go every(ctx, 5*time.Second, func() {
		n, err := e.Probe.Tick(ctx)
		logf("probe", err)
		if err == nil && n > 0 {
			logf("probe source", e.Store.SourceOK(ctx, "probe:"+e.Probe.Vantage, "probe", 60, map[string]any{"last_batch": n}))
		}
	})
	go every(ctx, 30*time.Second, func() { logf("evaluate", e.Evaluate(ctx)) })
	go every(ctx, time.Hour, func() { logf("prune", e.Store.Prune(ctx)) })
	if e.GitHub != nil {
		go every(ctx, e.GitHub.Interval, func() { logf("github", e.GitHub.Sync(ctx)) })
	}
	<-ctx.Done()
}
