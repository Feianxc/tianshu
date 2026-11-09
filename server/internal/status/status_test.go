package status

import (
	"testing"
	"time"

	"github.com/Feianxc/tianshu/server/internal/model"
)

var now = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func sig(id, name, state string, age time.Duration, stale bool) model.Signal {
	return model.Signal{EntityID: id, Name: name, State: state, Summary: name + " " + state, ReceivedAt: now.Add(-age), ObservedAt: now.Add(-age), Stale: stale, Value: map[string]any{}}
}

func snap(e model.Entity, sigs ...model.Signal) *Snapshot {
	return NewSnapshot(now, []model.Entity{e}, map[string][]model.Signal{e.ID: sigs}, nil)
}

func device(avail string) model.Entity {
	return model.Entity{ID: "d", Kind: model.KindDevice, Lifecycle: model.LifeActive, Attrs: map[string]any{"availability": avail}}
}

func TestDeviceThatStoppedReporting(t *testing.T) {
	cases := []struct {
		name, avail, want string
	}{
		{"常开服务器失联不能算正常", "always_on", model.HealthUnknown},
		{"笔记本合盖是预期内", "intermittent", model.HealthOffline},
		{"事件型手机没上报是预期内", "event", model.HealthOffline},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := snap(device(c.avail), sig("d", "host.heartbeat", model.StateOK, time.Hour, true)).Health("d")
			if h.State != c.want || h.Freshness != model.FreshStale {
				t.Fatalf("got %s/%s", h.State, h.Freshness)
			}
		})
	}
}

func TestDeviceWithoutCollectorIsUnknownNotHealthy(t *testing.T) {
	h := snap(device("manual")).Health("d")
	if h.State != model.HealthUnknown || h.Freshness != model.FreshMissing {
		t.Fatalf("got %s/%s", h.State, h.Freshness)
	}
}

func service(life string) model.Entity {
	return model.Entity{ID: "s", Kind: model.KindService, Lifecycle: life, Attrs: map[string]any{}}
}

func TestPausedServiceIsNeverAnOutage(t *testing.T) {
	h := snap(service(model.LifePaused), sig("s", "unit:x", model.StateFail, time.Second, false)).Health("s")
	if h.State != model.HealthPaused {
		t.Fatalf("got %s", h.State)
	}
}

func TestPausedServiceStillAnsweringIsSaid(t *testing.T) {
	h := snap(service(model.LifePaused), sig("s", "check.http", model.StateOK, time.Second, false)).Health("s")
	if h.State != model.HealthPaused || h.Reason != "已暂停，但仍在响应" {
		t.Fatalf("got %s / %s", h.State, h.Reason)
	}
}

func TestFreshFailureBeatsGreenSignals(t *testing.T) {
	h := snap(service(model.LifeActive),
		sig("s", "unit:x", model.StateOK, time.Second, false),
		sig("s", "check.http", model.StateFail, time.Second, false)).Health("s")
	if h.State != model.HealthDown {
		t.Fatalf("got %s", h.State)
	}
}

func TestOldFailureIsNeitherAnOutageNorHealth(t *testing.T) {
	h := snap(service(model.LifeActive),
		sig("s", "unit:x", model.StateOK, time.Second, false),
		sig("s", "check.http", model.StateFail, time.Hour, true)).Health("s")
	if h.State != model.HealthUnknown || h.Freshness != FreshPartial {
		t.Fatalf("got %s/%s", h.State, h.Freshness)
	}
}

func TestOldPassIsNotHealthButDoesNotAccuse(t *testing.T) {
	h := snap(service(model.LifeActive),
		sig("s", "unit:x", model.StateOK, time.Second, false),
		sig("s", "check.http", model.StateOK, time.Hour, true)).Health("s")
	if h.State != model.HealthOK || h.Freshness != FreshPartial {
		t.Fatalf("got %s/%s", h.State, h.Freshness)
	}
}

func TestHostWithOnlyAHeartbeatLeftIsNotHealthy(t *testing.T) {
	h := snap(device("always_on"),
		sig("d", "host.heartbeat", model.StateOK, time.Second, false),
		sig("d", "host.disk:/", model.StateFail, time.Hour, true)).Health("d")
	if h.State != model.HealthUnknown {
		t.Fatalf("99%% 的磁盘过期后不能变成正常，得到 %s", h.State)
	}
}

func TestOldFailureEventuallyStopsCounting(t *testing.T) {
	h := snap(service(model.LifeActive),
		sig("s", "unit:x", model.StateOK, time.Second, false),
		sig("s", "check.http", model.StateFail, 90*24*time.Hour, true)).Health("s")
	if h.State != model.HealthOK || h.Freshness != FreshPartial {
		t.Fatalf("90 天前的失败不能永远把服务钉在未知: %s/%s", h.State, h.Freshness)
	}
}

func TestProjectWithPartlyVisibleServiceIsPartial(t *testing.T) {
	p := model.Entity{ID: "p", Kind: model.KindProject, Lifecycle: model.LifeActive, Attrs: map[string]any{}}
	a := model.Entity{ID: "a", Kind: model.KindService, Slug: "a", Lifecycle: model.LifeActive, Attrs: map[string]any{}}
	rels := []model.Relation{{FromID: "a", Type: model.RelPartOf, ToID: "p"}}
	sigs := map[string][]model.Signal{"a": {sig("a", "unit:x", model.StateOK, time.Second, false), sig("a", "check.http", model.StateOK, time.Hour, true)}}
	if h := NewSnapshot(now, []model.Entity{p, a}, sigs, rels).Health("p"); h.Freshness != FreshPartial {
		t.Fatalf("got %s/%s", h.State, h.Freshness)
	}
}

func TestProjectFreshnessFollowsItsServices(t *testing.T) {
	p := model.Entity{ID: "p", Kind: model.KindProject, Lifecycle: model.LifeActive, Attrs: map[string]any{}}
	a := model.Entity{ID: "a", Kind: model.KindService, Slug: "a", Lifecycle: model.LifeActive, Attrs: map[string]any{}}
	rels := []model.Relation{{FromID: "a", Type: model.RelPartOf, ToID: "p"}}
	s := NewSnapshot(now, []model.Entity{p, a}, map[string][]model.Signal{"a": {sig("a", "unit:x", model.StateOK, time.Hour, true)}}, rels)
	if h := s.Health("p"); h.Freshness != model.FreshStale || h.State != model.HealthUnknown {
		t.Fatalf("got %s/%s", h.State, h.Freshness)
	}
}

func TestStaleRepositoryClaimsNothing(t *testing.T) {
	r := model.Entity{ID: "r", Kind: model.KindRepository, Lifecycle: model.LifeActive, Attrs: map[string]any{}}
	h := snap(r, sig("r", "repo.meta", model.StateOK, time.Hour, true)).Health("r")
	if h.State != model.HealthUnknown || h.Freshness != model.FreshStale {
		t.Fatalf("got %s/%s", h.State, h.Freshness)
	}
}

func TestAllSignalsStaleMeansUnknown(t *testing.T) {
	h := snap(service(model.LifeActive), sig("s", "unit:x", model.StateOK, time.Hour, true)).Health("s")
	if h.State != model.HealthUnknown || h.Freshness != model.FreshStale {
		t.Fatalf("旧绿灯不能继续当正常: %s/%s", h.State, h.Freshness)
	}
}

func TestServiceWithNoSignals(t *testing.T) {
	h := snap(service(model.LifeActive)).Health("s")
	if h.State != model.HealthUnknown || h.Freshness != model.FreshMissing {
		t.Fatalf("got %s/%s", h.State, h.Freshness)
	}
}

func TestProjectRollsUpItsServices(t *testing.T) {
	p := model.Entity{ID: "p", Kind: model.KindProject, Lifecycle: model.LifeActive, Attrs: map[string]any{}}
	a := model.Entity{ID: "a", Kind: model.KindService, Slug: "a", Lifecycle: model.LifeActive, Attrs: map[string]any{}}
	b := model.Entity{ID: "b", Kind: model.KindService, Slug: "b", Lifecycle: model.LifePaused, Attrs: map[string]any{}}
	rels := []model.Relation{{FromID: "a", Type: model.RelPartOf, ToID: "p"}, {FromID: "b", Type: model.RelPartOf, ToID: "p"}}
	s := NewSnapshot(now, []model.Entity{p, a, b}, map[string][]model.Signal{"a": {sig("a", "unit:x", model.StateFail, time.Second, false)}}, rels)
	if h := s.Health("p"); h.State != model.HealthDown {
		t.Fatalf("got %s", h.State)
	}
	s = NewSnapshot(now, []model.Entity{p, b}, map[string][]model.Signal{}, rels[1:])
	if h := s.Health("p"); h.State != model.HealthPaused {
		t.Fatalf("全部暂停应显示暂停，got %s", h.State)
	}
}

func TestProjectWithoutServicesHasNoRuntimeClaim(t *testing.T) {
	p := model.Entity{ID: "p", Kind: model.KindProject, Lifecycle: model.LifeActive, Attrs: map[string]any{}}
	h := NewSnapshot(now, []model.Entity{p}, nil, nil).Health("p")
	if h.State != model.HealthUnknown || h.Freshness != model.FreshMissing {
		t.Fatalf("got %s/%s", h.State, h.Freshness)
	}
}
