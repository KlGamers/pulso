package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/KlGamers/pulso/internal/alert"
	"github.com/KlGamers/pulso/internal/cfg"
	"github.com/KlGamers/pulso/internal/sched"
	"github.com/KlGamers/pulso/internal/store"
	"github.com/KlGamers/pulso/internal/web"
)

func main() {
	path := flag.String("c", "pulso.json", "config file")
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *path, log, nil); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, path string, log *slog.Logger, ready chan<- string) error {
	c, err := cfg.Load(path)
	if err != nil {
		return err
	}
	st, err := store.Open(c.DB)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, stop := context.WithCancel(ctx)
	defer stop()

	var ntf *alert.Notifier
	if c.Alert.URL != "" {
		ntf = alert.NewNotifier(c.Alert.URL, c.Alert.Retries)
	}

	ln, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           web.New(c, st).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sched.New(c, st, ntf, log).Run(ctx)
	}()

	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", ln.Addr().String(), "targets", len(c.Targets))
		if ready != nil {
			ready <- ln.Addr().String()
		}
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err = <-errc:
		stop()
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(sctx)
	wg.Wait()
	return err
}
