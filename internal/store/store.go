package store

import (
	"context"
	"database/sql"
	"math"
	"sort"
	"time"

	_ "modernc.org/sqlite"

	"github.com/KlGamers/pulso/internal/probe"
)

const schema = `
CREATE TABLE IF NOT EXISTS checks (
	id INTEGER PRIMARY KEY,
	target TEXT NOT NULL,
	at INTEGER NOT NULL,
	up INTEGER NOT NULL,
	ms REAL NOT NULL,
	code INTEGER NOT NULL,
	err TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS checks_target_at ON checks(target, at);
CREATE TABLE IF NOT EXISTS incidents (
	id INTEGER PRIMARY KEY,
	target TEXT NOT NULL,
	start INTEGER NOT NULL,
	stop INTEGER,
	reason TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS incidents_target ON incidents(target, stop);
`

type Store struct{ db *sql.DB }

type Stats struct {
	Total  int     `json:"total"`
	Up     int     `json:"up"`
	Uptime float64 `json:"uptime"`
	Avg    float64 `json:"avg_ms"`
	P95    float64 `json:"p95_ms"`
}

type Incident struct {
	ID     int64      `json:"id"`
	Target string     `json:"target"`
	Start  time.Time  `json:"start"`
	Stop   *time.Time `json:"stop"`
	Reason string     `json:"reason"`
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Add(ctx context.Context, r probe.Res) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO checks(target, at, up, ms, code, err) VALUES(?,?,?,?,?,?)`,
		r.Target, r.At.UnixMilli(), r.Up, r.Ms, r.Code, r.Err)
	return err
}

func (s *Store) Recent(ctx context.Context, target string, n int) ([]probe.Res, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT at, up, ms, code, err FROM checks WHERE target=? ORDER BY at DESC LIMIT ?`, target, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]probe.Res, 0, n)
	for rows.Next() {
		r := probe.Res{Target: target}
		var at int64
		if err := rows.Scan(&at, &r.Up, &r.Ms, &r.Code, &r.Err); err != nil {
			return nil, err
		}
		r.At = time.UnixMilli(at).UTC()
		out = append(out, r)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

func (s *Store) Stats(ctx context.Context, target string, since time.Time) (Stats, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT up, ms FROM checks WHERE target=? AND at>=?`, target, since.UnixMilli())
	if err != nil {
		return Stats{}, err
	}
	defer rows.Close()
	st := Stats{}
	var lat []float64
	var sum float64
	for rows.Next() {
		var up bool
		var ms float64
		if err := rows.Scan(&up, &ms); err != nil {
			return Stats{}, err
		}
		st.Total++
		if up {
			st.Up++
			lat = append(lat, ms)
			sum += ms
		}
	}
	if st.Total > 0 {
		st.Uptime = 100 * float64(st.Up) / float64(st.Total)
	}
	if len(lat) > 0 {
		st.Avg = sum / float64(len(lat))
		st.P95 = Pct(lat, 95)
	}
	return st, rows.Err()
}

func Pct(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	idx := int(math.Ceil(p/100*float64(len(c)))) - 1
	if idx < 0 {
		idx = 0
	}
	return c[idx]
}

func (s *Store) OpenIncident(ctx context.Context, target string, at time.Time, reason string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO incidents(target, start, reason)
		 SELECT ?,?,? WHERE NOT EXISTS (SELECT 1 FROM incidents WHERE target=? AND stop IS NULL)`,
		target, at.UnixMilli(), reason, target)
	return err
}

func (s *Store) CloseIncident(ctx context.Context, target string, at time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE incidents SET stop=? WHERE target=? AND stop IS NULL`, at.UnixMilli(), target)
	return err
}

func (s *Store) Down(ctx context.Context, target string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM incidents WHERE target=? AND stop IS NULL`, target).Scan(&n)
	return n > 0, err
}

func (s *Store) Incidents(ctx context.Context, n int) ([]Incident, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, target, start, stop, reason FROM incidents ORDER BY start DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Incident{}
	for rows.Next() {
		var in Incident
		var start int64
		var stop sql.NullInt64
		if err := rows.Scan(&in.ID, &in.Target, &start, &stop, &in.Reason); err != nil {
			return nil, err
		}
		in.Start = time.UnixMilli(start).UTC()
		if stop.Valid {
			t := time.UnixMilli(stop.Int64).UTC()
			in.Stop = &t
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

func (s *Store) Prune(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM checks WHERE at<?`, before.UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
