package alert

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KlGamers/pulso/internal/probe"
)

func res(up bool) probe.Res {
	return probe.Res{Target: "api", At: time.Now(), Up: up, Err: "boom"}
}

func TestTrackerThresholds(t *testing.T) {
	tr := NewTracker(3, 2)
	seq := []struct {
		up   bool
		want Kind
	}{
		{true, ""}, {false, ""}, {false, ""}, {true, ""},
		{false, ""}, {false, ""}, {false, Down}, {false, ""},
		{true, ""}, {false, ""}, {true, ""}, {true, Up}, {true, ""},
	}
	for i, s := range seq {
		ev := tr.Feed(res(s.up))
		var got Kind
		if ev != nil {
			got = ev.Kind
		}
		if got != s.want {
			t.Fatalf("step %d: got %q want %q", i, got, s.want)
		}
	}
}

func TestTrackerSeed(t *testing.T) {
	tr := NewTracker(1, 1)
	tr.Seed("api", true)
	if ev := tr.Feed(res(false)); ev != nil {
		t.Fatalf("already down, got %+v", ev)
	}
	if ev := tr.Feed(res(true)); ev == nil || ev.Kind != Up {
		t.Fatalf("want up, got %+v", ev)
	}
}

func TestNotifierRetries(t *testing.T) {
	var hits atomic.Int32
	var got Event
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		json.NewDecoder(r.Body).Decode(&got)
	}))
	defer srv.Close()
	n := NewNotifier(srv.URL, 3)
	n.SetWait(time.Millisecond)
	if err := n.Send(context.Background(), Event{Kind: Down, Target: "api", Reason: "boom"}); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 3 || got.Target != "api" || got.Kind != Down {
		t.Fatalf("hits=%d got=%+v", hits.Load(), got)
	}
}

func TestNotifierGivesUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	n := NewNotifier(srv.URL, 2)
	n.SetWait(time.Millisecond)
	if err := n.Send(context.Background(), Event{Kind: Down}); err == nil {
		t.Fatal("want error")
	}
	var none *Notifier
	if err := none.Send(context.Background(), Event{}); err != nil {
		t.Fatal(err)
	}
}
