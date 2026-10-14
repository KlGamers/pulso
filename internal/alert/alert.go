package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/KlGamers/pulso/internal/probe"
)

type Kind string

const (
	Down Kind = "down"
	Up   Kind = "up"
)

type Event struct {
	Kind   Kind      `json:"kind"`
	Target string    `json:"target"`
	At     time.Time `json:"at"`
	Reason string    `json:"reason,omitempty"`
	Fails  int       `json:"fails,omitempty"`
}

type state struct {
	down  bool
	fails int
	oks   int
}

type Tracker struct {
	mu      sync.Mutex
	st      map[string]*state
	failAft int
	recAft  int
}

func NewTracker(failAft, recAft int) *Tracker {
	return &Tracker{st: map[string]*state{}, failAft: failAft, recAft: recAft}
}

func (t *Tracker) Seed(target string, down bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.st[target] = &state{down: down}
}

func (t *Tracker) Feed(r probe.Res) *Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.st[r.Target]
	if !ok {
		s = &state{}
		t.st[r.Target] = s
	}
	if r.Up {
		s.fails = 0
		s.oks++
		if s.down && s.oks >= t.recAft {
			s.down = false
			return &Event{Kind: Up, Target: r.Target, At: r.At}
		}
		return nil
	}
	s.oks = 0
	s.fails++
	if !s.down && s.fails >= t.failAft {
		s.down = true
		return &Event{Kind: Down, Target: r.Target, At: r.At, Reason: r.Err, Fails: s.fails}
	}
	return nil
}

type Notifier struct {
	url     string
	retries int
	wait    time.Duration
	cli     *http.Client
}

func NewNotifier(url string, retries int) *Notifier {
	return &Notifier{url: url, retries: retries, wait: time.Second, cli: &http.Client{Timeout: 10 * time.Second}}
}

func (n *Notifier) SetWait(d time.Duration) { n.wait = d }

func (n *Notifier) Send(ctx context.Context, ev Event) error {
	if n == nil || n.url == "" {
		return nil
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	var last error
	for i := 0; i < n.retries; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(n.wait << (i - 1)):
			}
		}
		if last = n.post(ctx, body); last == nil {
			return nil
		}
	}
	return fmt.Errorf("webhook failed after %d tries: %w", n.retries, last)
}

func (n *Notifier) post(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := n.cli.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("status %d", res.StatusCode)
	}
	return nil
}
