package sched

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KlGamers/pulso/internal/alert"
	"github.com/KlGamers/pulso/internal/cfg"
	"github.com/KlGamers/pulso/internal/store"
)

func TestDownAndRecover(t *testing.T) {
	var healthy atomic.Bool
	healthy.Store(true)
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer app.Close()

	var mu sync.Mutex
	var evs []alert.Event
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev alert.Event
		json.NewDecoder(r.Body).Decode(&ev)
		mu.Lock()
		evs = append(evs, ev)
		mu.Unlock()
	}))
	defer hook.Close()

	c := &cfg.Cfg{
		Keep:    cfg.Dur(time.Hour),
		Workers: 2,
		Alert:   cfg.Alert{URL: hook.URL, FailAfter: 2, RecoverAfter: 2, Retries: 1},
		Targets: []cfg.Target{{Name: "app", Kind: "http", Addr: app.URL, Every: cfg.Dur(time.Second), Timeout: cfg.Dur(time.Second)}},
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New(c, st, alert.NewNotifier(hook.URL, 1), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	tg := c.Targets[0]

	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(evs)
	}
	wait := func(n int) {
		t.Helper()
		end := time.Now().Add(2 * time.Second)
		for count() < n {
			if time.Now().After(end) {
				t.Fatalf("want %d events, got %d", n, count())
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	s.once(ctx, tg)
	healthy.Store(false)
	s.once(ctx, tg)
	if count() != 0 {
		t.Fatal("alerted before threshold")
	}
	s.once(ctx, tg)
	wait(1)
	if down, _ := st.Down(ctx, "app"); !down {
		t.Fatal("incident not opened")
	}
	healthy.Store(true)
	s.once(ctx, tg)
	s.once(ctx, tg)
	wait(2)
	if down, _ := st.Down(ctx, "app"); down {
		t.Fatal("incident not closed")
	}
	mu.Lock()
	defer mu.Unlock()
	if evs[0].Kind != alert.Down || evs[1].Kind != alert.Up || evs[0].Fails != 2 {
		t.Fatalf("events: %+v", evs)
	}
	stt, _ := st.Stats(ctx, "app", time.Now().Add(-time.Hour))
	if stt.Total != 5 || stt.Up != 3 {
		t.Fatalf("stats: %+v", stt)
	}
}

func TestRunStops(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer app.Close()
	c := &cfg.Cfg{
		Keep:    cfg.Dur(time.Hour),
		Workers: 1,
		Alert:   cfg.Alert{FailAfter: 1, RecoverAfter: 1},
		Targets: []cfg.Target{{Name: "app", Kind: "http", Addr: app.URL, Every: cfg.Dur(time.Second), Timeout: cfg.Dur(time.Second)}},
	}
	st, _ := store.Open(filepath.Join(t.TempDir(), "t.db"))
	defer st.Close()
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		New(c, st, nil, slog.New(slog.NewTextHandler(io.Discard, nil))).Run(ctx)
		close(done)
	}()
	end := time.Now().Add(3 * time.Second)
	for {
		if rec, _ := st.Recent(context.Background(), "app", 1); len(rec) == 1 {
			break
		}
		if time.Now().After(end) {
			t.Fatal("no check recorded")
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not stop")
	}
}

func TestStoreErrorsAreLoggedNotFatal(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer app.Close()
	c := &cfg.Cfg{
		Keep:    cfg.Dur(time.Hour),
		Workers: 1,
		Alert:   cfg.Alert{FailAfter: 1, RecoverAfter: 1},
		Targets: []cfg.Target{{Name: "app", Kind: "http", Addr: app.URL, Every: cfg.Dur(time.Second), Timeout: cfg.Dur(time.Second)}},
	}
	st, _ := store.Open(filepath.Join(t.TempDir(), "t.db"))
	st.Close()
	var buf bytes.Buffer
	var mu sync.Mutex
	log := slog.New(slog.NewTextHandler(&lockedWriter{w: &buf, mu: &mu}, nil))
	s := New(c, st, nil, log)
	s.once(context.Background(), c.Targets[0])
	ctx, stop := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer stop()
	s.Run(ctx)
	mu.Lock()
	out := buf.String()
	mu.Unlock()
	for _, want := range []string{"msg=store", "msg=incident", "msg=seed", "msg=prune"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in log:\n%s", want, out)
		}
	}
}

func TestCancelWhileWaitingForWorker(t *testing.T) {
	c := &cfg.Cfg{Workers: 1, Alert: cfg.Alert{FailAfter: 1, RecoverAfter: 1}}
	st, _ := store.Open(filepath.Join(t.TempDir(), "t.db"))
	defer st.Close()
	s := New(c, st, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.sem <- struct{}{}
	ctx, stop := context.WithCancel(context.Background())
	stop()
	s.once(ctx, cfg.Target{Name: "x", Kind: "tcp", Addr: "127.0.0.1:1", Timeout: cfg.Dur(time.Second)})
	if rec, _ := st.Recent(context.Background(), "x", 1); len(rec) != 0 {
		t.Fatal("cancelled check was stored")
	}
}

func TestJitterBounds(t *testing.T) {
	for _, name := range []string{"a", "api", "a much longer target name"} {
		if j := jitter(name, time.Minute); j < 0 || j >= 10*time.Second {
			t.Fatalf("%s: %s", name, j)
		}
		if j := jitter(name, 2*time.Second); j >= 2*time.Second {
			t.Fatalf("%s: %s", name, j)
		}
	}
	if jitter("api", time.Minute) != jitter("api", time.Minute) {
		t.Fatal("jitter must be deterministic")
	}
}

type lockedWriter struct {
	w  *bytes.Buffer
	mu *sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
