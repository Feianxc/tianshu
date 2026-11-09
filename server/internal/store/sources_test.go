package store

import (
	"context"
	"testing"
)

func TestMovingTheHubForgetsTheOldProbeVantage(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	for _, s := range []struct{ name, kind string }{{"probe:laptop", "probe"}, {"probe:server", "probe"}, {"agent:laptop", "agent"}} {
		if err := st.SourceOK(ctx, s.name, s.kind, 60, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.DropOtherProbeSources(ctx, "probe:server"); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListSources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, s := range list {
		got[s.Name] = true
	}
	if got["probe:laptop"] || !got["probe:server"] || !got["agent:laptop"] {
		t.Fatalf("只应删掉别处的探测来源，采集器来源不能动: %v", got)
	}
}
