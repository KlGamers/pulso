package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func conf(t *testing.T, listen, target string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "pulso.json")
	body := `{"listen":"` + listen + `","db":"` + filepath.Join(dir, "p.db") + `","alert":{"url":"http://127.0.0.1:1/hook"},"targets":[{"name":"app","addr":"` + target + `","every":"1s","timeout":"1s"}]}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunServesAndStops(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer app.Close()
	ctx, stop := context.WithCancel(context.Background())
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() { done <- run(ctx, conf(t, "127.0.0.1:0", app.URL), quiet(), ready) }()

	var addr string
	select {
	case addr = <-ready:
	case err := <-done:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not start")
	}
	res, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "ok" {
		t.Fatalf("healthz: %q", body)
	}
	end := time.Now().Add(5 * time.Second)
	for {
		res, err := http.Get("http://" + addr + "/api/targets")
		if err == nil {
			raw, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if strings.Contains(string(raw), `"state":"up"`) {
				break
			}
		}
		if time.Now().After(end) {
			t.Fatal("target never reported up")
		}
		time.Sleep(50 * time.Millisecond)
	}
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after cancel")
	}
}

func TestRunErrors(t *testing.T) {
	if err := run(context.Background(), filepath.Join(t.TempDir(), "missing.json"), quiet(), nil); err == nil {
		t.Fatal("want error for missing config")
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte(`{"targets":[]}`), 0o644)
	if err := run(context.Background(), bad, quiet(), nil); err == nil || !strings.Contains(err.Error(), "no targets") {
		t.Fatalf("got %v", err)
	}
	nodb := filepath.Join(t.TempDir(), "nodb.json")
	os.WriteFile(nodb, []byte(`{"db":"/nonexistent/dir/p.db","targets":[{"name":"a","addr":"http://x.io"}]}`), 0o644)
	if err := run(context.Background(), nodb, quiet(), nil); err == nil {
		t.Fatal("want error for unwritable db")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if err := run(context.Background(), conf(t, ln.Addr().String(), "http://x.io"), quiet(), nil); err == nil {
		t.Fatal("want error when the port is taken")
	}
}
