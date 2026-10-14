package probe

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/KlGamers/pulso/internal/cfg"
)

const maxBody = 1 << 20

type Res struct {
	Target string    `json:"target"`
	At     time.Time `json:"at"`
	Up     bool      `json:"up"`
	Ms     float64   `json:"ms"`
	Code   int       `json:"code"`
	Err    string    `json:"err,omitempty"`
}

type Prober struct {
	cli *http.Client
	dl  net.Dialer
}

func New() *Prober {
	tr := &http.Transport{
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	return &Prober{cli: &http.Client{
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("stopped after %d redirects", len(via))
			}
			return nil
		},
	}}
}

func (p *Prober) Run(ctx context.Context, t cfg.Target) Res {
	ctx, stop := context.WithTimeout(ctx, t.Timeout.Std())
	defer stop()
	r := Res{Target: t.Name, At: time.Now().UTC()}
	t0 := time.Now()
	var err error
	switch t.Kind {
	case "tcp":
		err = p.tcp(ctx, t)
	default:
		r.Code, err = p.http(ctx, t)
	}
	r.Ms = float64(time.Since(t0).Microseconds()) / 1000
	if err != nil {
		r.Err = trim(err.Error())
		return r
	}
	r.Up = true
	return r
}

func (p *Prober) tcp(ctx context.Context, t cfg.Target) error {
	c, err := p.dl.DialContext(ctx, "tcp", t.Addr)
	if err != nil {
		return err
	}
	return c.Close()
}

func (p *Prober) http(ctx context.Context, t cfg.Target) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.Addr, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "pulso/1")
	res, err := p.cli.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
	if err != nil {
		return res.StatusCode, err
	}
	if t.Expect != 0 && res.StatusCode != t.Expect {
		return res.StatusCode, fmt.Errorf("status %d, want %d", res.StatusCode, t.Expect)
	}
	if t.Expect == 0 && (res.StatusCode < 200 || res.StatusCode >= 400) {
		return res.StatusCode, fmt.Errorf("status %d", res.StatusCode)
	}
	if t.Match != "" && !strings.Contains(string(body), t.Match) {
		return res.StatusCode, fmt.Errorf("body missing %q", t.Match)
	}
	return res.StatusCode, nil
}

func trim(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}
