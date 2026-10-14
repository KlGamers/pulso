package sched

import (
	"context"
	"hash/fnv"
	"log/slog"
	"sync"
	"time"

	"github.com/KlGamers/pulso/internal/alert"
	"github.com/KlGamers/pulso/internal/cfg"
	"github.com/KlGamers/pulso/internal/probe"
	"github.com/KlGamers/pulso/internal/store"
)

type Sched struct {
	c   *cfg.Cfg
	st  *store.Store
	pr  *probe.Prober
	trk *alert.Tracker
	ntf *alert.Notifier
	log *slog.Logger
	sem chan struct{}
}

func New(c *cfg.Cfg, st *store.Store, ntf *alert.Notifier, log *slog.Logger) *Sched {
	return &Sched{
		c:   c,
		st:  st,
		pr:  probe.New(),
		trk: alert.NewTracker(c.Alert.FailAfter, c.Alert.RecoverAfter),
		ntf: ntf,
		log: log,
		sem: make(chan struct{}, c.Workers),
	}
}

func (s *Sched) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, t := range s.c.Targets {
		down, err := s.st.Down(ctx, t.Name)
		if err != nil {
			s.log.Error("seed", "target", t.Name, "err", err)
		}
		s.trk.Seed(t.Name, down)
		wg.Add(1)
		go func(t cfg.Target) {
			defer wg.Done()
			s.loop(ctx, t)
		}(t)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.prune(ctx)
	}()
	wg.Wait()
}

func (s *Sched) loop(ctx context.Context, t cfg.Target) {
	every := t.Every.Std()
	select {
	case <-ctx.Done():
		return
	case <-time.After(jitter(t.Name, every)):
	}
	tk := time.NewTicker(every)
	defer tk.Stop()
	for {
		s.once(ctx, t)
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
	}
}

func (s *Sched) once(ctx context.Context, t cfg.Target) {
	select {
	case s.sem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	r := s.pr.Run(ctx, t)
	<-s.sem
	if ctx.Err() != nil {
		return
	}
	if err := s.st.Add(ctx, r); err != nil {
		s.log.Error("store", "target", t.Name, "err", err)
	}
	ev := s.trk.Feed(r)
	if ev == nil {
		return
	}
	s.log.Warn("state", "target", ev.Target, "kind", ev.Kind, "reason", ev.Reason)
	var err error
	if ev.Kind == alert.Down {
		err = s.st.OpenIncident(ctx, ev.Target, ev.At, ev.Reason)
	} else {
		err = s.st.CloseIncident(ctx, ev.Target, ev.At)
	}
	if err != nil {
		s.log.Error("incident", "target", ev.Target, "err", err)
	}
	go func(ev alert.Event) {
		if err := s.ntf.Send(ctx, ev); err != nil {
			s.log.Error("notify", "target", ev.Target, "err", err)
		}
	}(*ev)
}

func (s *Sched) prune(ctx context.Context) {
	tk := time.NewTicker(time.Hour)
	defer tk.Stop()
	for {
		n, err := s.st.Prune(ctx, time.Now().Add(-s.c.Keep.Std()))
		if err != nil && ctx.Err() == nil {
			s.log.Error("prune", "err", err)
		} else if n > 0 {
			s.log.Info("prune", "rows", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
		}
	}
}

func jitter(name string, every time.Duration) time.Duration {
	h := fnv.New32a()
	h.Write([]byte(name))
	max := every
	if max > 10*time.Second {
		max = 10 * time.Second
	}
	return time.Duration(h.Sum32()) % max
}
