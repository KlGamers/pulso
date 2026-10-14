package cfg

import (
	"strings"
	"testing"
	"time"
)

func TestParseDefaults(t *testing.T) {
	c, err := Parse([]byte(`{"targets":[{"name":"api","addr":"https://example.com/health"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	tg := c.Targets[0]
	if tg.Kind != "http" || tg.Every.Std() != 30*time.Second || tg.Timeout.Std() != 5*time.Second {
		t.Fatalf("defaults not applied: %+v", tg)
	}
	if c.Listen != ":8080" || c.Workers != 8 || c.Alert.FailAfter != 3 || c.Alert.RecoverAfter != 2 {
		t.Fatalf("defaults not applied: %+v", c)
	}
}

func TestParseDurations(t *testing.T) {
	c, err := Parse([]byte(`{"keep":"48h","targets":[{"name":"db","kind":"tcp","addr":"127.0.0.1:5432","every":"10s","timeout":"2s"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Keep.Std() != 48*time.Hour || c.Targets[0].Every.Std() != 10*time.Second {
		t.Fatalf("bad durations: %+v", c)
	}
}

func TestParseRejects(t *testing.T) {
	bad := map[string]string{
		"no targets":   `{"targets":[]}`,
		"duplicate":    `{"targets":[{"name":"a","addr":"http://x.io"},{"name":"a","addr":"http://y.io"}]}`,
		"unknown kind": `{"targets":[{"name":"a","kind":"icmp","addr":"x"}]}`,
		"invalid http": `{"targets":[{"name":"a","addr":"ftp://x.io"}]}`,
		"every":        `{"targets":[{"name":"a","addr":"http://x.io","every":"10ms"}]}`,
		"timeout":      `{"targets":[{"name":"a","addr":"http://x.io","every":"5s","timeout":"9s"}]}`,
		"without name": `{"targets":[{"addr":"http://x.io"}]}`,
		"alert url":    `{"alert":{"url":"nope"},"targets":[{"name":"a","addr":"http://x.io"}]}`,
		"parse":        `{`,
	}
	for want, raw := range bad {
		_, err := Parse([]byte(raw))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
}
