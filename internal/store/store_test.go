package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/KlGamers/pulso/internal/probe"
)

func open(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestStatsAndRecent(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for i := 0; i < 100; i++ {
		r := probe.Res{Target: "api", At: now.Add(-time.Duration(100-i) * time.Minute), Up: i%10 != 0, Ms: float64(i + 1), Code: 200}
		if err := st.Add(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	s, err := st.Stats(ctx, "api", now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if s.Total != 100 || s.Up != 90 || s.Uptime != 90 {
		t.Fatalf("stats: %+v", s)
	}
	if s.P95 != 96 {
		t.Fatalf("p95: %v", s.P95)
	}
	half, _ := st.Stats(ctx, "api", now.Add(-50*time.Minute-time.Second))
	if half.Total != 50 {
		t.Fatalf("window: %+v", half)
	}
	rec, err := st.Recent(ctx, "api", 5)
	if err != nil || len(rec) != 5 {
		t.Fatalf("recent: %v %d", err, len(rec))
	}
	if !rec[0].At.Before(rec[4].At) || rec[4].Ms != 100 {
		t.Fatalf("order: %+v", rec)
	}
	if e, _ := st.Stats(ctx, "none", now.Add(-time.Hour)); e.Total != 0 || e.Uptime != 0 {
		t.Fatalf("empty: %+v", e)
	}
}

func TestPct(t *testing.T) {
	v := []float64{5, 1, 4, 2, 3}
	if Pct(v, 50) != 3 || Pct(v, 100) != 5 || Pct(v, 1) != 1 || Pct(nil, 95) != 0 {
		t.Fatal("percentile")
	}
	if v[0] != 5 {
		t.Fatal("input mutated")
	}
}

func TestIncidents(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	t0 := time.Now().UTC().Truncate(time.Millisecond)
	st.OpenIncident(ctx, "api", t0, "status 500")
	st.OpenIncident(ctx, "api", t0.Add(time.Second), "again")
	if down, _ := st.Down(ctx, "api"); !down {
		t.Fatal("want down")
	}
	st.CloseIncident(ctx, "api", t0.Add(time.Minute))
	if down, _ := st.Down(ctx, "api"); down {
		t.Fatal("want up")
	}
	inc, err := st.Incidents(ctx, 10)
	if err != nil || len(inc) != 1 {
		t.Fatalf("incidents: %v %+v", err, inc)
	}
	if inc[0].Reason != "status 500" || inc[0].Stop == nil || inc[0].Stop.Sub(inc[0].Start) != time.Minute {
		t.Fatalf("incident: %+v", inc[0])
	}
}

func TestPrune(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	now := time.Now()
	st.Add(ctx, probe.Res{Target: "a", At: now.Add(-48 * time.Hour), Up: true})
	st.Add(ctx, probe.Res{Target: "a", At: now, Up: true})
	n, err := st.Prune(ctx, now.Add(-24*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("prune: %v %d", err, n)
	}
}

func TestErrorsAfterClose(t *testing.T) {
	if _, err := Open("/nonexistent/dir/x.db"); err == nil {
		t.Fatal("want error opening in a missing dir")
	}
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	ctx := context.Background()
	now := time.Now()
	if err := st.Add(ctx, probe.Res{Target: "a", At: now}); err == nil {
		t.Error("add")
	}
	if _, err := st.Recent(ctx, "a", 1); err == nil {
		t.Error("recent")
	}
	if _, err := st.Stats(ctx, "a", now); err == nil {
		t.Error("stats")
	}
	if _, err := st.Incidents(ctx, 1); err == nil {
		t.Error("incidents")
	}
	if _, err := st.Prune(ctx, now); err == nil {
		t.Error("prune")
	}
	if _, err := st.Down(ctx, "a"); err == nil {
		t.Error("down")
	}
}

func TestPctSingle(t *testing.T) {
	if Pct([]float64{7}, 0) != 7 {
		t.Fatal("p0 of one value")
	}
}
