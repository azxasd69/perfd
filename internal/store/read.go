package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// StandRow — строка списка стендов.
type StandRow struct {
	Slug            string
	Name            string
	Platform        string
	OS              string
	CPU             string
	DeployMode      string
	Visibility      string
	PassportMissing bool
	GenerationID    int64
	Index           float64
	ScenariosUsed   int
	LowData         bool
	BestScenario    string
	BestDeltaPct    float64
	WorstScenario   string
	WorstDeltaPct   float64
	LastMeasuredAt  int64
	LastSeen        int64
}

// ScenarioRow — замер сценария с дельтой к базе.
type ScenarioRow struct {
	Code           string
	Representation string
	Unit           string
	MedianMs       float64
	PerUnitMs      float64
	MinMs          float64
	MaxMs          float64
	Portion        float64
	Repeats        int
	MeasuredAt     int64
	DeltaPct       *float64
}

// GenerationRow — поколение стенда.
type GenerationRow struct {
	ID              int64
	Fingerprint     string
	Platform        string
	OS              string
	CPU              string
	DeployMode      string
	ServerName      string
	FirstSeen       int64
	LastSeen        int64
	PassportMissing bool
}

// HistoryPoint — точка временного ряда.
type HistoryPoint struct {
	MeasuredAt int64
	MedianMs   float64
	PerUnitMs  float64
	MinMs      float64
	MaxMs      float64
	Samples    int
}

// TopRow — строка топа худших сценариев.
type TopRow struct {
	Slug       string
	Scenario   string
	Platform   string
	PerUnitMs  float64
	MedianMs   float64
	MeasuredAt int64
}

// CompareRow — сравнение двух стендов по сценарию.
type CompareRow struct {
	Scenario      string
	APerUnitMs    *float64
	BPerUnitMs    *float64
	DeltaPct      *float64
}

// CurrentGeneration возвращает актуальное поколение стенда (последнее по last_seen).
func (s *Store) CurrentGeneration(ctx context.Context, slug string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM stand_generations WHERE stand_id = ? ORDER BY last_seen DESC, id DESC LIMIT 1`, slug).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

const standSelect = `
SELECT st.id, st.name, COALESCE(g.platform, st.platform), COALESCE(g.os, st.os),
       COALESCE(g.cpu, st.cpu), COALESCE(g.deploy_mode, st.deploy_mode), st.visibility,
       COALESCE(g.passport_missing, 1), COALESCE(g.id, 0), st.last_seen
  FROM stands st
  LEFT JOIN stand_generations g ON g.id = (
        SELECT id FROM stand_generations WHERE stand_id = st.id ORDER BY last_seen DESC, id DESC LIMIT 1)`

const aggSelect = `
  LEFT JOIN aggregates a ON a.generation_id = g.id AND a.computed_at = (
        SELECT MAX(computed_at) FROM aggregates WHERE generation_id = g.id)`

// ListStands отдаёт стенды; includePrivate=false скрывает visibility='private'.
func (s *Store) ListStands(ctx context.Context, includePrivate bool) ([]StandRow, error) {
	q := `SELECT st.id, st.name, COALESCE(g.platform, st.platform, ''), COALESCE(g.os, st.os, ''),
	             COALESCE(g.cpu, st.cpu, ''), COALESCE(g.deploy_mode, st.deploy_mode, ''), st.visibility,
	             COALESCE(g.passport_missing, 1), COALESCE(g.id, 0), st.last_seen,
	             COALESCE(a.index_value, 0), COALESCE(a.scenarios_used, 0),
	             COALESCE(a.best_scenario, ''), COALESCE(a.best_delta_pct, 0),
	             COALESCE(a.worst_scenario, ''), COALESCE(a.worst_delta_pct, 0),
	             COALESCE(a.last_measured_at, 0)
	        FROM stands st
	        LEFT JOIN stand_generations g ON g.id = (
	              SELECT id FROM stand_generations WHERE stand_id = st.id ORDER BY last_seen DESC, id DESC LIMIT 1)
	        LEFT JOIN aggregates a ON a.generation_id = g.id AND a.computed_at = (
	              SELECT MAX(computed_at) FROM aggregates WHERE generation_id = g.id)
	        WHERE (? = 1 OR st.visibility = 'public')
	        ORDER BY COALESCE(a.index_value, -1) DESC, st.id`
	rows, err := s.db.QueryContext(ctx, q, boolInt(includePrivate))
	if err != nil {
		return nil, fmt.Errorf("список стендов: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []StandRow
	for rows.Next() {
		var r StandRow
		if err := rows.Scan(&r.Slug, &r.Name, &r.Platform, &r.OS, &r.CPU, &r.DeployMode, &r.Visibility,
			&r.PassportMissing, &r.GenerationID, &r.LastSeen, &r.Index, &r.ScenariosUsed,
			&r.BestScenario, &r.BestDeltaPct, &r.WorstScenario, &r.WorstDeltaPct, &r.LastMeasuredAt); err != nil {
			return nil, err
		}
		r.LowData = r.ScenariosUsed < 5
		out = append(out, r)
	}
	return out, rows.Err()
}

// Stand отдаёт один стенд по slug; при visibility='private' и публичном запросе — ErrNotFound.
func (s *Store) Stand(ctx context.Context, slug string, includePrivate bool) (StandRow, error) {
	q := `SELECT st.id, st.name, COALESCE(g.platform, st.platform, ''), COALESCE(g.os, st.os, ''),
	             COALESCE(g.cpu, st.cpu, ''), COALESCE(g.deploy_mode, st.deploy_mode, ''), st.visibility,
	             COALESCE(g.passport_missing, 1), COALESCE(g.id, 0), st.last_seen,
	             COALESCE(a.index_value, 0), COALESCE(a.scenarios_used, 0),
	             COALESCE(a.best_scenario, ''), COALESCE(a.best_delta_pct, 0),
	             COALESCE(a.worst_scenario, ''), COALESCE(a.worst_delta_pct, 0),
	             COALESCE(a.last_measured_at, 0)
	        FROM stands st
	        LEFT JOIN stand_generations g ON g.id = (
	              SELECT id FROM stand_generations WHERE stand_id = st.id ORDER BY last_seen DESC, id DESC LIMIT 1)
	        LEFT JOIN aggregates a ON a.generation_id = g.id AND a.computed_at = (
	              SELECT MAX(computed_at) FROM aggregates WHERE generation_id = g.id)
	        WHERE st.id = ? AND (? = 1 OR st.visibility = 'public')`
	var r StandRow
	err := s.db.QueryRowContext(ctx, q, slug, boolInt(includePrivate)).Scan(
		&r.Slug, &r.Name, &r.Platform, &r.OS, &r.CPU, &r.DeployMode, &r.Visibility,
		&r.PassportMissing, &r.GenerationID, &r.LastSeen, &r.Index, &r.ScenariosUsed,
		&r.BestScenario, &r.BestDeltaPct, &r.WorstScenario, &r.WorstDeltaPct, &r.LastMeasuredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	r.LowData = r.ScenariosUsed < 5
	return r, err
}

// Scenarios отдаёт замеры поколения с дельтой к базе сравнения.
func (s *Store) Scenarios(ctx context.Context, genID int64) ([]ScenarioRow, error) {
	baseID, err := baseIDFor(ctx, s.db)
	if err != nil {
		return nil, err
	}
	q := `SELECT r.scenario, COALESCE(sc.representation, ''), r.unit, r.median_ms, r.per_unit_ms,
	             COALESCE(r.min_ms, 0), COALESCE(r.max_ms, 0), r.portion, r.repeats, r.measured_at,
	             b.per_unit_ms
	        FROM results r
	        LEFT JOIN scenarios sc ON sc.code = r.scenario
	        LEFT JOIN results b ON b.scenario = r.scenario AND b.generation_id = ?
	       WHERE r.generation_id = ?
	       ORDER BY r.scenario`
	rows, err := s.db.QueryContext(ctx, q, baseID, genID)
	if err != nil {
		return nil, fmt.Errorf("сценарии стенда: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []ScenarioRow
	for rows.Next() {
		var r ScenarioRow
		var basePer sql.NullFloat64
		if err := rows.Scan(&r.Code, &r.Representation, &r.Unit, &r.MedianMs, &r.PerUnitMs,
			&r.MinMs, &r.MaxMs, &r.Portion, &r.Repeats, &r.MeasuredAt, &basePer); err != nil {
			return nil, err
		}
		if basePer.Valid && basePer.Float64 > 0 && r.PerUnitMs > 0 {
			delta := (basePer.Float64 - r.PerUnitMs) / basePer.Float64 * 100
			d := round1(delta)
			r.DeltaPct = &d
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Passport отдаёт текущий паспорт поколения как JSON-строку.
func (s *Store) Passport(ctx context.Context, genID int64) (string, int, int64, error) {
	var props, schema, captured int64
	var body string
	err := s.db.QueryRowContext(ctx,
		`SELECT props_json, schema, captured_at FROM stand_passports
		  WHERE generation_id = ? ORDER BY is_current DESC, captured_at DESC LIMIT 1`, genID).
		Scan(&body, &schema, &captured)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, 0, ErrNotFound
	}
	_ = props
	return body, int(schema), captured, err
}

// Generations отдаёт все поколения стенда.
func (s *Store) Generations(ctx context.Context, slug string) ([]GenerationRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, fingerprint, platform, os, cpu, deploy_mode, server_name, first_seen, last_seen, passport_missing
		   FROM stand_generations WHERE stand_id = ? ORDER BY id DESC`, slug)
	if err != nil {
		return nil, fmt.Errorf("поколения стенда: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []GenerationRow
	for rows.Next() {
		var g GenerationRow
		if err := rows.Scan(&g.ID, &g.Fingerprint, &g.Platform, &g.OS, &g.CPU, &g.DeployMode,
			&g.ServerName, &g.FirstSeen, &g.LastSeen, &g.PassportMissing); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// HistoryDay отдаёт детальный ряд (последние 12 месяцев по retention).
func (s *Store) HistoryDay(ctx context.Context, genID int64, scenario string, from, to int64, limit int) ([]HistoryPoint, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT measured_at, median_ms, per_unit_ms, COALESCE(min_ms,0), COALESCE(max_ms,0)
		   FROM history
		  WHERE generation_id = ? AND scenario = ? AND (? = 0 OR measured_at >= ?) AND (? = 0 OR measured_at <= ?)
		  ORDER BY measured_at DESC LIMIT ?`,
		genID, scenario, from, from, to, to, limit)
	if err != nil {
		return nil, fmt.Errorf("история замеров: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []HistoryPoint
	for rows.Next() {
		var p HistoryPoint
		if err := rows.Scan(&p.MeasuredAt, &p.MedianMs, &p.PerUnitMs, &p.MinMs, &p.MaxMs); err != nil {
			return nil, err
		}
		p.Samples = 1
		out = append(out, p)
	}
	return out, rows.Err()
}

// HistoryMonth отдаёт месячные свёртки (бессрочно, ТЗ Р-19).
func (s *Store) HistoryMonth(ctx context.Context, genID int64, scenario string) ([]HistoryPoint, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT month, median_ms, per_unit_ms, min_ms, max_ms, samples
		   FROM history_monthly WHERE generation_id = ? AND scenario = ? ORDER BY month`, genID, scenario)
	if err != nil {
		return nil, fmt.Errorf("месячные свёртки: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []HistoryPoint
	for rows.Next() {
		var p HistoryPoint
		if err := rows.Scan(&p.MeasuredAt, &p.MedianMs, &p.PerUnitMs, &p.MinMs, &p.MaxMs, &p.Samples); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Top отдаёт худшие сценарии за период по per_unit_ms.
func (s *Store) Top(ctx context.Context, days, limit int, includePrivate bool) ([]TopRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if days <= 0 {
		days = 30
	}
	since := time.Now().AddDate(0, 0, -days).Unix()
	rows, err := s.db.QueryContext(ctx,
		`SELECT st.id, h.scenario, COALESCE(g.platform, ''), h.per_unit_ms, h.median_ms, h.measured_at
		   FROM history h
		   JOIN stand_generations g ON g.id = h.generation_id
		   JOIN stands st ON st.id = g.stand_id
		  WHERE h.measured_at >= ? AND (? = 1 OR st.visibility = 'public')
		  ORDER BY h.per_unit_ms DESC LIMIT ?`, since, boolInt(includePrivate), limit)
	if err != nil {
		return nil, fmt.Errorf("топ худших: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []TopRow
	for rows.Next() {
		var r TopRow
		if err := rows.Scan(&r.Slug, &r.Scenario, &r.Platform, &r.PerUnitMs, &r.MedianMs, &r.MeasuredAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Compare сравнивает два стенда по общим сценариям.
func (s *Store) Compare(ctx context.Context, a, b string) ([]CompareRow, int, int, error) {
	genA, err := s.CurrentGeneration(ctx, a)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("стенд %s: %w", a, err)
	}
	genB, err := s.CurrentGeneration(ctx, b)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("стенд %s: %w", b, err)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT scenario, MAX(a_per) AS a_per, MAX(b_per) AS b_per FROM (
		    SELECT scenario, per_unit_ms AS a_per, NULL AS b_per FROM results WHERE generation_id = ?
		    UNION ALL
		    SELECT scenario, NULL AS a_per, per_unit_ms AS b_per FROM results WHERE generation_id = ?
		 ) GROUP BY scenario ORDER BY scenario`, genA, genB)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("сравнение стендов: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []CompareRow
	common, onlyA, onlyB := 0, 0, 0
	for rows.Next() {
		var r CompareRow
		var av, bv sql.NullFloat64
		if err := rows.Scan(&r.Scenario, &av, &bv); err != nil {
			return nil, 0, 0, err
		}
		if av.Valid {
			v := av.Float64
			r.APerUnitMs = &v
		}
		if bv.Valid {
			v := bv.Float64
			r.BPerUnitMs = &v
		}
		switch {
		case av.Valid && bv.Valid:
			common++
			if av.Float64 > 0 {
				d := round1((av.Float64 - bv.Float64) / av.Float64 * 100)
				r.DeltaPct = &d
			}
		case av.Valid:
			onlyA++
		default:
			onlyB++
		}
		out = append(out, r)
	}
	return out, common, onlyA + onlyB, rows.Err()
}

// ChannelsHealth — состояние каналов для /healthz/channels.
func (s *Store) ChannelsHealth(ctx context.Context, channels []string) (map[string]map[string]any, error) {
	out := make(map[string]map[string]any, len(channels))
	for _, ch := range channels {
		var cnt int64
		var last sql.NullInt64
		if err := s.db.QueryRowContext(ctx,
			"SELECT COUNT(*), MAX(received_at) FROM ingest_log WHERE channel = ?", ch).Scan(&cnt, &last); err != nil {
			return nil, err
		}
		out[ch] = map[string]any{"enabled": true, "accepted": cnt, "last_at": last.Int64}
	}
	return out, nil
}

// StatsForReady — числа для /readyz.
func (s *Store) StatsForReady(ctx context.Context) (Counts, error) { return s.Counts(ctx) }

func baseIDFor(ctx context.Context, db *sql.DB) (int64, error) {
	var id int64
	err := db.QueryRowContext(ctx,
		`SELECT id FROM stand_generations WHERE instr(stand_key, 'ендор') > 0 OR instr(stand_key, 'ЕНДОР') > 0 ORDER BY id LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func round1(v float64) float64 {
	return float64(int64(v*10+copySign(0.5, v))) / 10
}

func copySign(v, sign float64) float64 {
	if sign < 0 {
		return -v
	}
	return v
}
