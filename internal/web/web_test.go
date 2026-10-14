package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KlGamers/pulso/internal/cfg"
	"github.com/KlGamers/pulso/internal/probe"
	"github.com/KlGamers/pulso/internal/store"
)

func setup(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c := &cfg.Cfg{Title: "Acme <Status>", Targets: []cfg.Target{{Name: "api", Kind: "http"}, {Name: "db", Kind: "tcp"}}}
	return New(c, st).Handler(), st
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestPageAndAPI(t *testing.T) {
	h, st := setup(t)
	ctx := context.Background()
	now := time.Now().UTC()
	st.Add(ctx, probe.Res{Target: "api", At: now.Add(-time.Minute), Up: true, Ms: 40, Code: 200})
	st.Add(ctx, probe.Res{Target: "api", At: now, Up: false, Ms: 12, Code: 502, Err: "status 502 <b>"})
	st.OpenIncident(ctx, "api", now, "status 502")

	w := get(h, "/")
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "Acme &lt;Status&gt;") || !strings.Contains(body, "Some systems are having trouble") {
		t.Fatalf("page: %d", w.Code)
	}
	if strings.Contains(body, "status 502 <b>") || !strings.Contains(body, "ongoing") {
		t.Fatal("page escaping or incident missing")
	}

	var rows []Row
	if err := json.Unmarshal(get(h, "/api/targets").Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].State != "down" || rows[1].State != "pending" || rows[0].Day.Uptime != 50 {
		t.Fatalf("targets: %+v", rows)
	}

	var rec []probe.Res
	json.Unmarshal(get(h, "/api/targets/api/checks?n=1").Body.Bytes(), &rec)
	if len(rec) != 1 || rec[0].Code != 502 {
		t.Fatalf("checks: %+v", rec)
	}
	if get(h, "/api/targets/nope/checks").Code != 404 || get(h, "/missing").Code != 404 {
		t.Fatal("want 404")
	}
	if get(h, "/healthz").Body.String() != "ok" {
		t.Fatal("healthz")
	}
	var inc []store.Incident
	json.Unmarshal(get(h, "/api/incidents").Body.Bytes(), &inc)
	if len(inc) != 1 || inc[0].Stop != nil {
		t.Fatalf("incidents: %+v", inc)
	}
}

func TestSpan(t *testing.T) {
	for d, want := range map[time.Duration]string{45 * time.Second: "45s", 5 * time.Minute: "5m", 3 * time.Hour: "3h", 72 * time.Hour: "3d"} {
		if got := span(d); got != want {
			t.Errorf("%v: got %s want %s", d, got, want)
		}
	}
}

func TestStoreFailuresReturn500(t *testing.T) {
	h, st := setup(t)
	st.Close()
	for _, p := range []string{"/", "/api/targets", "/api/targets/api/checks", "/api/incidents"} {
		w := get(h, p)
		if w.Code != http.StatusInternalServerError {
			t.Errorf("%s: got %d", p, w.Code)
		}
		if p != "/" && !strings.Contains(w.Body.String(), `"error"`) {
			t.Errorf("%s: body %q", p, w.Body.String())
		}
	}
}

func TestAllUpAndFormatting(t *testing.T) {
	h, st := setup(t)
	ctx := context.Background()
	now := time.Now().UTC()
	st.Add(ctx, probe.Res{Target: "api", At: now, Up: true, Ms: 3.42, Code: 200})
	st.Add(ctx, probe.Res{Target: "db", At: now, Up: true, Ms: 0.4})
	body := get(h, "/").Body.String()
	if !strings.Contains(body, "All systems operational") || !strings.Contains(body, "<b>3.4</b> ms avg") {
		t.Fatal("all-up page")
	}
	if got := get(h, "/api/targets/api/checks?n=5000").Code; got != 200 {
		t.Fatal(got)
	}
	if ms(0) != "0" || ms(12.6) != "13" || ms(3.42) != "3.4" {
		t.Fatal("ms format")
	}
}
