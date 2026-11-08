package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"perfd/internal/model"
)

// Итоговые статусы конверта (ТЗ § 7.3).
const (
	StatusOK        = "ok"
	StatusDuplicate = "duplicate"
	StatusConflict  = "conflict"
	StatusRejected  = "rejected"
)

// EnvelopeInput — что приходит от транспорта в единый конвейер.
type EnvelopeInput struct {
	Envelope   model.Envelope
	Channel    string
	RemoteAddr string
	RequestID  string
	Formula    int
}

// EnvelopeOutcome — результат по одному конверту.
type EnvelopeOutcome struct {
	ID           string
	Status       string
	Code         int
	Error        string
	Message      string
	Issue        string
	GenerationID int64
	StandSlug    string
}

// Ingest прогоняет один конверт через конвейер приёма (ТЗ § 4.2, шаги 3–9).
// Транзакция — на конверт (решение Р-11): отказ одного не откатывает другие.
func (s *Store) Ingest(ctx context.Context, in EnvelopeInput) (EnvelopeOutcome, error) {
	e := in.Envelope
	out := EnvelopeOutcome{ID: e.ID, StandSlug: e.Stand.Key}

	if err := e.Validate(); err != nil {
		out.Status, out.Code, out.Error = StatusRejected, 400, "invalid_envelope"
		if errors.Is(err, model.ErrUnsupportedSchema) {
			out.Code, out.Error = 415, "unsupported_schema"
		}
		out.Message = err.Error()
		// Отказ фиксируется в журнале, чтобы 1С видела причину.
		_ = s.logRejected(ctx, in, out)
		return out, nil
	}

	issues := e.Normalize()
	bodySHA := e.BodySHA256()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, fmt.Errorf("начало транзакции: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Шаг 6: идемпотентность по id + хешу тела.
	var prevStatus, prevSHA string
	var prevGen sql.NullInt64
	err = tx.QueryRowContext(ctx,
		"SELECT status, body_sha256, generation_id FROM ingest_log WHERE id = ?", e.ID).
		Scan(&prevStatus, &prevSHA, &prevGen)
	switch {
	case err == nil && prevStatus == StatusOK && prevSHA == bodySHA:
		_ = tx.Rollback()
		out.Status, out.Code, out.GenerationID = StatusDuplicate, 200, prevGen.Int64
		return out, nil
	case err == nil && prevStatus == StatusOK && prevSHA != bodySHA:
		_ = tx.Rollback()
		out.Status, out.Code, out.Error = StatusConflict, 409, "conflict"
		out.Message = "тот же id с другим телом"
		return out, nil
	case err == nil:
		// Ранее был отказ (rejected/error): разрешаем повторную попытку.
		if _, err := tx.ExecContext(ctx, "DELETE FROM ingest_log WHERE id = ?", e.ID); err != nil {
			return out, fmt.Errorf("сброс отказавшего конверта: %w", err)
		}
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return out, fmt.Errorf("проверка идемпотентности: %w", err)
	}

	now := time.Now().Unix()

	// Шаг 5 (часть 1): стенд и поколение.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO stands (id, name, fingerprint, platform, visibility, first_seen, last_seen)
		 VALUES (?, ?, ?, ?, 'private', ?, ?)
		 ON CONFLICT(id) DO UPDATE SET last_seen = excluded.last_seen, fingerprint = excluded.fingerprint`,
		e.Stand.Key, e.Stand.Key, e.Stand.Fingerprint, e.Stand.Platform, now, now); err != nil {
		return out, fmt.Errorf("стенд %s: %w", e.Stand.Key, err)
	}

	genID, err := ensureGeneration(ctx, tx, e, now)
	if err != nil {
		return out, err
	}
	out.GenerationID = genID

	// Шаг 5 (часть 2): паспорт.
	if e.Kind == model.KindPassport && e.Passport != nil {
		if issue, err := savePassport(ctx, tx, genID, e, now); err != nil {
			return out, err
		} else if issue != "" {
			issues = append(issues, issue)
		}
	}

	// Шаги 7–8: замеры и история.
	switch e.Kind {
	case model.KindResult:
		if err := saveResults(ctx, tx, genID, e, now); err != nil {
			return out, err
		}
		if err := saveHistory(ctx, tx, genID, e.Results, e.MeasuredAtUTC, now); err != nil {
			return out, err
		}
	case model.KindHistory:
		if err := saveHistory(ctx, tx, genID, e.History, e.MeasuredAtUTC, now); err != nil {
			return out, err
		}
	}

	// Шаг 9: журнал приёма.
	issue := strings.Join(dedupe(issues), ",")
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO ingest_log (id, stand_id, kind, status, http_code, bytes, error, remote_addr, request_id, received_at, channel, body_sha256, issue, generation_id)
		 VALUES (?, ?, ?, ?, ?, ?, '', ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.Stand.Key, e.Kind, StatusOK, 200, len(bodySHA), in.RemoteAddr, in.RequestID, now,
		in.Channel, bodySHA, issue, genID); err != nil {
		return out, fmt.Errorf("журнал приёма: %w", err)
	}

	// Агрегаты пересчитываем в той же транзакции (ТЗ ФТ-08).
	if err := recomputeAggregates(ctx, tx, genID, in.Formula); err != nil {
		return out, err
	}

	if err := tx.Commit(); err != nil {
		return out, fmt.Errorf("коммит приёма: %w", err)
	}

	out.Status, out.Code, out.Issue = StatusOK, 200, issue
	return out, nil
}

func (s *Store) logRejected(ctx context.Context, in EnvelopeInput, out EnvelopeOutcome) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO ingest_log (id, stand_id, kind, status, http_code, bytes, error, remote_addr, request_id, received_at, channel, body_sha256, issue, generation_id)
		 VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, '', '', NULL)`,
		emptyIfInvalid(out.ID), in.Envelope.Stand.Key, in.Envelope.Kind, StatusRejected, out.Code,
		out.Error, in.RemoteAddr, in.RequestID, time.Now().Unix(), in.Channel)
	return err
}

// emptyIfInvalid: id может не пройти проверку — тогда ключом журнала станет заглушка,
// чтобы битый конверт не мешал приёму следующих.
func emptyIfInvalid(id string) string {
	if id == "" {
		return "invalid-" + fmt.Sprint(time.Now().UnixNano())
	}
	return id
}

// ensureGeneration ищет поколение по отпечатку и создаёт новое при смене среды.
func ensureGeneration(ctx context.Context, tx *sql.Tx, e model.Envelope, now int64) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, "SELECT id FROM stand_generations WHERE fingerprint = ?", e.Stand.Fingerprint).Scan(&id)
	if err == nil {
		_, err = tx.ExecContext(ctx, "UPDATE stand_generations SET last_seen = ? WHERE id = ?", now, id)
		return id, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("поиск поколения: %w", err)
	}

	var parent sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		"SELECT MAX(id) FROM stand_generations WHERE stand_id = ?", e.Stand.Key).Scan(&parent); err != nil {
		return 0, fmt.Errorf("поиск предыдущего поколения: %w", err)
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO stand_generations (stand_id, fingerprint, stand_key, parent_generation_id, platform, first_seen, last_seen)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.Stand.Key, e.Stand.Fingerprint, e.Stand.Key, nullableInt(parent), e.Stand.Platform, now, now)
	if err != nil {
		return 0, fmt.Errorf("создание поколения: %w", err)
	}
	return res.LastInsertId()
}

// savePassport пишет версию паспорта и обновляет извлечённые поля поколения.
func savePassport(ctx context.Context, tx *sql.Tx, genID int64, e model.Envelope, now int64) (string, error) {
	propsJSON, err := json.Marshal(e.Passport.Properties)
	if err != nil {
		return "", fmt.Errorf("сериализация паспорта: %w", err)
	}
	issue := ""
	masked, found := maskPassword(e.Passport.Properties)
	if found {
		issue = "password_in_fields"
		propsJSON, _ = json.Marshal(masked)
	}

	if _, err := tx.ExecContext(ctx,
		"UPDATE stand_passports SET is_current = 0 WHERE generation_id = ?", genID); err != nil {
		return issue, fmt.Errorf("сброс текущего паспорта: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO stand_passports (stand_id, fingerprint, captured_at, generation_id, schema, props_json, body_sha256, is_current)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 1)`,
		e.Stand.Key, e.Stand.Fingerprint, derefInt(e.CapturedAtUTC), genID, e.Passport.Schema, string(propsJSON), e.BodySHA256()); err != nil {
		return issue, fmt.Errorf("запись паспорта: %w", err)
	}

	p := e.Passport.Properties
	serverName := str(p["СерверКомпьютер"])
	osName := str(p["СерверОС"])
	cpu := str(p["СерверЦПУ"])
	threads := num(p["СерверЦПУПотоки"])
	if threads == 0 {
		threads = num(p["СерверПотоков"])
	}
	ram := num(p["СерверПамятьМб"])
	mode := str(p["РежимИБ"])
	if mode == "" {
		mode = str(p["ВариантРазвёртывания"])
	}
	dbms := str(p["СУБД"])
	platform := e.Stand.Platform

	if _, err := tx.ExecContext(ctx,
		`UPDATE stand_generations SET platform = ?, os = ?, cpu = ?, threads = ?, ram_mb = ?,
		   deploy_mode = ?, dbms = ?, server_name = ?, passport_missing = 0, last_seen = ?
		 WHERE id = ?`,
		platform, osName, cpu, threads, ram, mode, dbms, serverName, now, genID); err != nil {
		return issue, fmt.Errorf("обновление поколения по паспорту: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE stands SET name = ?, platform = ?, os = ?, cpu = ?, deploy_mode = ? WHERE id = ?",
		firstNonEmpty(serverName, e.Stand.Key), platform, osName, cpu, mode, e.Stand.Key); err != nil {
		return issue, fmt.Errorf("обновление стенда по паспорту: %w", err)
	}
	return issue, nil
}

func saveResults(ctx context.Context, tx *sql.Tx, genID int64, e model.Envelope, now int64) error {
	for _, r := range e.Results {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO results (generation_id, scenario, platform, median_ms, per_unit_ms, unit, portion, repeats, min_ms, max_ms, measured_at, received_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(generation_id, scenario) DO UPDATE SET
			   platform = excluded.platform, median_ms = excluded.median_ms, per_unit_ms = excluded.per_unit_ms,
			   unit = excluded.unit, portion = excluded.portion, repeats = excluded.repeats,
			   min_ms = excluded.min_ms, max_ms = excluded.max_ms, measured_at = excluded.measured_at,
			   received_at = excluded.received_at`,
			genID, r.Scenario, e.Stand.Platform, r.MedianMs, round3(r.PerUnitMs), r.Unit, r.Portion, r.Repeats,
			r.MinMs, r.MaxMs, derefInt(e.MeasuredAtUTC), now); err != nil {
			return fmt.Errorf("замер %s: %w", r.Scenario, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO scenarios (code, representation, unit, default_portion) VALUES (?, ?, ?, ?)
			 ON CONFLICT(code) DO UPDATE SET representation = excluded.representation, unit = excluded.unit`,
			r.Scenario, r.Representation, r.Unit, r.Portion); err != nil {
			return fmt.Errorf("справочник сценариев %s: %w", r.Scenario, err)
		}
	}
	return nil
}

func saveHistory(ctx context.Context, tx *sql.Tx, genID int64, items []model.Result, fallback *int64, now int64) error {
	for _, r := range items {
		ts := derefInt(r.MeasuredAtUTC)
		if ts == 0 {
			ts = derefInt(fallback)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO history (generation_id, scenario, measured_at, median_ms, per_unit_ms, min_ms, max_ms, portion, unit, repeats, received_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(generation_id, scenario, measured_at) DO NOTHING`,
			genID, r.Scenario, ts, r.MedianMs, round3(r.PerUnitMs), r.MinMs, r.MaxMs, r.Portion, r.Unit, r.Repeats, now); err != nil {
			return fmt.Errorf("история %s: %w", r.Scenario, err)
		}
	}
	return nil
}

// recomputeAggregates считает индекс ИИ ровно по формуле 1С (ТЗ Р-08):
// 100 × среднее(база/стенд) по парным сценариям, округление до 1 знака.
func recomputeAggregates(ctx context.Context, tx *sql.Tx, genID int64, formula int) error {
	if formula <= 0 {
		formula = 1
	}
	baseID, err := baseGeneration(ctx, tx)
	if err != nil {
		return err
	}

	var (
		sum, bestDelta, worstDelta float64
		bestScenario, worstScenario string
		count                      int
		lastMeasured               int64
	)
	bestDelta, worstDelta = math.Inf(-1), math.Inf(1)

	if baseID != 0 && baseID != genID {
		rows, err := tx.QueryContext(ctx,
			`SELECT r.scenario, b.per_unit_ms, r.per_unit_ms, r.measured_at
			   FROM results r
			   JOIN results b ON b.scenario = r.scenario AND b.generation_id = ?
			  WHERE r.generation_id = ? AND r.per_unit_ms > 0 AND b.per_unit_ms > 0`,
			baseID, genID)
		if err != nil {
			return fmt.Errorf("выборка пар для индекса: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var scenario string
			var basePer, standPer float64
			var measured int64
			if err := rows.Scan(&scenario, &basePer, &standPer, &measured); err != nil {
				return err
			}
			sum += basePer / standPer
			count++
			delta := (basePer - standPer) / basePer * 100
			if delta > bestDelta {
				bestDelta, bestScenario = delta, scenario
			}
			if delta < worstDelta {
				worstDelta, worstScenario = delta, scenario
			}
			if measured > lastMeasured {
				lastMeasured = measured
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
	}

	index := 0.0
	if count > 0 {
		index = math.Round(sum/float64(count)*100*10) / 10
	} else {
		bestDelta, worstDelta = 0, 0
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO aggregates (generation_id, base_generation_id, formula, index_value, scenarios_used,
		    best_scenario, best_delta_pct, worst_scenario, worst_delta_pct, last_measured_at, computed_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(generation_id, base_generation_id, formula) DO UPDATE SET
		   index_value = excluded.index_value, scenarios_used = excluded.scenarios_used,
		   best_scenario = excluded.best_scenario, best_delta_pct = excluded.best_delta_pct,
		   worst_scenario = excluded.worst_scenario, worst_delta_pct = excluded.worst_delta_pct,
		   last_measured_at = excluded.last_measured_at, computed_at = excluded.computed_at`,
		genID, baseID, formula, index, count, bestScenario, bestDelta, worstScenario, worstDelta,
		nullableInt64(lastMeasured), time.Now().Unix()); err != nil {
		return fmt.Errorf("запись агрегата: %w", err)
	}
	return nil
}

// baseGeneration выбирает базу сравнения: стенд «вендор» (как в кокпите),
// иначе 0 — тогда индекс равен нулю и это видно в ответе.
func baseGeneration(ctx context.Context, tx *sql.Tx) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx,
		`SELECT g.id FROM stand_generations g
		  WHERE instr(g.stand_key, 'ендор') > 0 OR instr(g.stand_key, 'ЕНДОР') > 0
		  ORDER BY g.id LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("выбор базы сравнения: %w", err)
	}
	return id, nil
}

func maskPassword(props map[string]any) (map[string]any, bool) {
	found := false
	out := make(map[string]any, len(props))
	for k, v := range props {
		if s, ok := v.(string); ok && strings.Contains(strings.ToLower(s), "pwd=") && !strings.Contains(s, "Pwd=***") {
			idx := strings.Index(strings.ToLower(s), "pwd=")
			out[k] = s[:idx] + "Pwd=***"
			found = true
			continue
		}
		out[k] = v
	}
	return out, found
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func num(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int64:
		return t
	case json.Number:
		n, _ := t.Int64()
		return n
	}
	return 0
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func nullableInt(v sql.NullInt64) any {
	if v.Valid {
		return v.Int64
	}
	return nil
}

func nullableInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func derefInt(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := in[:0]
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
