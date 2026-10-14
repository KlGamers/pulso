package cfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"time"
)

type Dur time.Duration

func (d *Dur) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Dur(v)
	return nil
}

func (d Dur) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d Dur) Std() time.Duration { return time.Duration(d) }

type Target struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Addr    string `json:"addr"`
	Every   Dur    `json:"every"`
	Timeout Dur    `json:"timeout"`
	Expect  int    `json:"expect"`
	Match   string `json:"match"`
}

type Alert struct {
	URL          string `json:"url"`
	FailAfter    int    `json:"fail_after"`
	RecoverAfter int    `json:"recover_after"`
	Retries      int    `json:"retries"`
}

type Cfg struct {
	Listen  string   `json:"listen"`
	DB      string   `json:"db"`
	Title   string   `json:"title"`
	Keep    Dur      `json:"keep"`
	Workers int      `json:"workers"`
	Alert   Alert    `json:"alert"`
	Targets []Target `json:"targets"`
}

func Load(path string) (*Cfg, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(raw)
}

func Parse(raw []byte) (*Cfg, error) {
	c := &Cfg{}
	if err := json.Unmarshal(raw, c); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	c.defaults()
	if err := c.check(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Cfg) defaults() {
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.DB == "" {
		c.DB = "pulso.db"
	}
	if c.Title == "" {
		c.Title = "Status"
	}
	if c.Keep == 0 {
		c.Keep = Dur(30 * 24 * time.Hour)
	}
	if c.Workers <= 0 {
		c.Workers = 8
	}
	if c.Alert.FailAfter <= 0 {
		c.Alert.FailAfter = 3
	}
	if c.Alert.RecoverAfter <= 0 {
		c.Alert.RecoverAfter = 2
	}
	if c.Alert.Retries <= 0 {
		c.Alert.Retries = 3
	}
	for i := range c.Targets {
		t := &c.Targets[i]
		if t.Kind == "" {
			t.Kind = "http"
		}
		if t.Every == 0 {
			t.Every = Dur(30 * time.Second)
		}
		if t.Timeout == 0 {
			t.Timeout = Dur(5 * time.Second)
		}
	}
}

func (c *Cfg) check() error {
	if len(c.Targets) == 0 {
		return errors.New("no targets")
	}
	seen := map[string]bool{}
	for _, t := range c.Targets {
		if t.Name == "" {
			return errors.New("target without name")
		}
		if seen[t.Name] {
			return fmt.Errorf("duplicate target %q", t.Name)
		}
		seen[t.Name] = true
		if t.Every.Std() < time.Second {
			return fmt.Errorf("%s: every must be >= 1s", t.Name)
		}
		if t.Timeout.Std() > t.Every.Std() {
			return fmt.Errorf("%s: timeout exceeds every", t.Name)
		}
		switch t.Kind {
		case "http":
			u, err := url.Parse(t.Addr)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return fmt.Errorf("%s: invalid http addr %q", t.Name, t.Addr)
			}
		case "tcp":
			if t.Addr == "" {
				return fmt.Errorf("%s: empty tcp addr", t.Name)
			}
		default:
			return fmt.Errorf("%s: unknown kind %q", t.Name, t.Kind)
		}
	}
	if c.Alert.URL != "" {
		if u, err := url.Parse(c.Alert.URL); err != nil || u.Host == "" {
			return fmt.Errorf("invalid alert url %q", c.Alert.URL)
		}
	}
	return nil
}
