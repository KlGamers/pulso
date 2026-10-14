package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
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
	if err := run(*path, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(path string, log *slog.Logger) error {
	c, err := cfg.Load(path)
	if err != nil {
		return err
	}
	st, err := store.Open(c.DB)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var ntf *alert.Notifier
	if c.Alert.URL != "" {
		ntf = alert.NewNotifier(c.Alert.URL, c.Alert.Retries)
	}

	srv := &http.Server{
		Addr:              c.Listen,
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
		log.Info("listening", "addr", c.Listen, "targets", len(c.Targets))
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
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
