package probe

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KlGamers/pulso/internal/cfg"
)

func target(addr string) cfg.Target {
	return cfg.Target{Name: "t", Kind: "http", Addr: addr, Timeout: cfg.Dur(time.Second)}
}

func TestHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.Write([]byte(`{"status":"ok"}`))
		case "/teapot":
			w.WriteHeader(http.StatusTeapot)
		case "/slow":
			select {
			case <-r.Context().Done():
			case <-time.After(3 * time.Second):
			}
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	p := New()

	tg := target(srv.URL + "/ok")
	if r := p.Run(context.Background(), tg); !r.Up || r.Code != 200 || r.Ms <= 0 {
		t.Fatalf("ok: %+v", r)
	}

	tg.Match = `"status":"ok"`
	if r := p.Run(context.Background(), tg); !r.Up {
		t.Fatalf("match: %+v", r)
	}
	tg.Match = "missing"
	if r := p.Run(context.Background(), tg); r.Up || !strings.Contains(r.Err, "body missing") {
		t.Fatalf("mismatch: %+v", r)
	}

	if r := p.Run(context.Background(), target(srv.URL+"/boom")); r.Up || r.Code != 500 {
		t.Fatalf("500: %+v", r)
	}

	tp := target(srv.URL + "/teapot")
	if r := p.Run(context.Background(), tp); r.Up {
		t.Fatalf("teapot default: %+v", r)
	}
	tp.Expect = http.StatusTeapot
	if r := p.Run(context.Background(), tp); !r.Up {
		t.Fatalf("teapot expected: %+v", r)
	}

	sl := target(srv.URL + "/slow")
	sl.Timeout = cfg.Dur(100 * time.Millisecond)
	t0 := time.Now()
	if r := p.Run(context.Background(), sl); r.Up || time.Since(t0) > time.Second {
		t.Fatalf("timeout: %+v", r)
	}
}

func TestTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	p := New()
	tg := cfg.Target{Name: "t", Kind: "tcp", Addr: addr, Timeout: cfg.Dur(time.Second)}
	if r := p.Run(context.Background(), tg); !r.Up {
		t.Fatalf("open port: %+v", r)
	}
	ln.Close()
	if r := p.Run(context.Background(), tg); r.Up || r.Err == "" {
		t.Fatalf("closed port: %+v", r)
	}
}

func TestRedirectLimitAndErrors(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/loop" {
			http.Redirect(w, r, srv.URL+"/loop", http.StatusFound)
			return
		}
		if r.URL.Path == "/hop" {
			http.Redirect(w, r, srv.URL+"/ok", http.StatusFound)
			return
		}
	}))
	defer srv.Close()
	p := New()
	if r := p.Run(context.Background(), target(srv.URL+"/hop")); !r.Up || r.Code != 200 {
		t.Fatalf("redirect: %+v", r)
	}
	if r := p.Run(context.Background(), target(srv.URL+"/loop")); r.Up || !strings.Contains(r.Err, "stopped after 5 redirects") {
		t.Fatalf("loop: %+v", r)
	}
	if r := p.Run(context.Background(), target("http://[::1]:namedport")); r.Up || r.Err == "" {
		t.Fatalf("bad url: %+v", r)
	}
	if got := trim(strings.Repeat("x", 500)); len(got) != 200 {
		t.Fatalf("trim: %d", len(got))
	}
}
