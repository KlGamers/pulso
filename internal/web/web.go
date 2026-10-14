package web

import (
	"context"
	_ "embed"
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
	"time"

	"github.com/KlGamers/pulso/internal/cfg"
	"github.com/KlGamers/pulso/internal/probe"
	"github.com/KlGamers/pulso/internal/store"
)

//go:embed page.html
var page string

var tpl = template.Must(template.New("page").Funcs(template.FuncMap{
	"ms":  ms,
	"pct": func(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) },
	"dur": span,
}).Parse(page))

type Row struct {
	Name   string      `json:"name"`
	Kind   string      `json:"kind"`
	State  string      `json:"state"`
	Last   *probe.Res  `json:"last"`
	Day    store.Stats `json:"day"`
	Week   store.Stats `json:"week"`
	Recent []probe.Res `json:"recent,omitempty"`
}

type view struct {
	Title     string
	Now       time.Time
	AllUp     bool
	Rows      []Row
	Incidents []store.Incident
}

type Srv struct {
	c  *cfg.Cfg
	st *store.Store
}

func New(c *cfg.Cfg, st *store.Store) *Srv { return &Srv{c: c, st: st} }

func (s *Srv) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /api/targets", s.targets)
	mux.HandleFunc("GET /api/targets/{name}/checks", s.checks)
	mux.HandleFunc("GET /api/incidents", s.incidents)
	return mux
}

func (s *Srv) rows(ctx context.Context, n int) ([]Row, error) {
	now := time.Now()
	out := make([]Row, 0, len(s.c.Targets))
	for _, t := range s.c.Targets {
		rec, err := s.st.Recent(ctx, t.Name, n)
		if err != nil {
			return nil, err
		}
		day, err := s.st.Stats(ctx, t.Name, now.Add(-24*time.Hour))
		if err != nil {
			return nil, err
		}
		week, err := s.st.Stats(ctx, t.Name, now.Add(-7*24*time.Hour))
		if err != nil {
			return nil, err
		}
		down, err := s.st.Down(ctx, t.Name)
		if err != nil {
			return nil, err
		}
		row := Row{Name: t.Name, Kind: t.Kind, State: "pending", Day: day, Week: week, Recent: rec}
		if len(rec) > 0 {
			row.Last = &rec[len(rec)-1]
			switch {
			case down:
				row.State = "down"
			case !row.Last.Up:
				row.State = "degraded"
			default:
				row.State = "up"
			}
		}
		out = append(out, row)
	}
	return out, nil
}

func (s *Srv) index(w http.ResponseWriter, r *http.Request) {
	rows, err := s.rows(r.Context(), 40)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	inc, err := s.st.Incidents(r.Context(), 10)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	v := view{Title: s.c.Title, Now: time.Now().UTC(), AllUp: true, Rows: rows, Incidents: inc}
	for _, row := range rows {
		if row.State == "down" || row.State == "degraded" {
			v.AllUp = false
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	tpl.Execute(w, v)
}

func (s *Srv) targets(w http.ResponseWriter, r *http.Request) {
	rows, err := s.rows(r.Context(), 1)
	if err != nil {
		fail(w, err)
		return
	}
	for i := range rows {
		rows[i].Recent = nil
	}
	reply(w, rows)
}

func (s *Srv) checks(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !s.known(name) {
		http.NotFound(w, r)
		return
	}
	n, err := strconv.Atoi(r.URL.Query().Get("n"))
	if err != nil || n <= 0 || n > 1000 {
		n = 100
	}
	rec, err := s.st.Recent(r.Context(), name, n)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, rec)
}

func (s *Srv) incidents(w http.ResponseWriter, r *http.Request) {
	inc, err := s.st.Incidents(r.Context(), 50)
	if err != nil {
		fail(w, err)
		return
	}
	reply(w, inc)
}

func (s *Srv) known(name string) bool {
	for _, t := range s.c.Targets {
		if t.Name == name {
			return true
		}
	}
	return false
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func ms(v float64) string {
	if v > 0 && v < 10 {
		return strconv.FormatFloat(v, 'f', 1, 64)
	}
	return strconv.FormatFloat(v, 'f', 0, 64)
}

func span(d time.Duration) string {
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	}
	return strconv.Itoa(int(d.Hours()/24)) + "d"
}
