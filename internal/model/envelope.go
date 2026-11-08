// Package model: конверт обмена schema 1 (ТЗ § 7.3) и его проверка.
// Здесь нет обращений к базе: только разбор, нормализация и правила,
// которые можно проверить без БД. Проверка справочника сценариев — в store.
package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Schema — единственная поддерживаемая версия конверта.
const Schema = 1

// Виды конвертов.
const (
	KindResult   = "result"
	KindPassport = "passport"
	KindHistory  = "history"
)

var (
	idRe          = regexp.MustCompile(`^(r|p|h)-[A-Za-z0-9_-]{43}$`)
	fingerprintRe = regexp.MustCompile(`^[a-f0-9]{64}$`)

	// ErrUnsupportedSchema — схема не наша; приёмник отвечает 415.
	ErrUnsupportedSchema = errors.New("unsupported_schema")
	// ErrInvalid — конверт не прошёл проверку; приёмник отвечает 207/400.
	ErrInvalid = errors.New("invalid_envelope")
)

// Client — сведения об отправителе (не обязательны для логики).
type Client struct {
	App      string `json:"app"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
}

// Stand — идентичность стенда: ключ, отпечаток, платформа.
type Stand struct {
	Key         string `json:"key"`
	Fingerprint string `json:"fingerprint"`
	Platform    string `json:"platform"`
}

// Result — один замер.
type Result struct {
	Scenario       string  `json:"scenario"`
	Representation string  `json:"representation"`
	MedianMs       float64 `json:"median_ms"`
	PerUnitMs      float64 `json:"per_unit_ms"`
	Unit           string  `json:"unit"`
	Portion        float64 `json:"portion"`
	Repeats        int     `json:"repeats"`
	MinMs          float64 `json:"min_ms"`
	MaxMs          float64 `json:"max_ms"`
	RunsString     string  `json:"runs_string"`
	MeasuredAtUTC  *int64  `json:"measured_at_utc"`
}

// Passport — паспорт стенда: схема и реквизиты как есть (ключи — имена 1С).
type Passport struct {
	Schema     int            `json:"schema"`
	Properties map[string]any `json:"properties"`
}

// Envelope — единица передачи.
type Envelope struct {
	Schema        int       `json:"schema"`
	ID            string    `json:"id"`
	Kind          string    `json:"kind"`
	Sig           string    `json:"sig"`
	Stand         Stand     `json:"stand"`
	MeasuredAt    string    `json:"measured_at"`
	MeasuredAtUTC *int64    `json:"measured_at_utc"`
	TZOffset      *int      `json:"tz_offset"`
	CapturedAt    string    `json:"captured_at"`
	CapturedAtUTC *int64    `json:"captured_at_utc"`
	RunID         string    `json:"run_id"`
	Results       []Result  `json:"results"`
	History       []Result  `json:"history"`
	Passport      *Passport `json:"passport"`
}

// Batch — тело запроса POST /api/v1/ingest.
type Batch struct {
	Client Client     `json:"client"`
	Batch  []Envelope `json:"batch"`
}

// BodySHA256 — sha256 hex тела конверта в каноническом виде (для идемпотентности).
// Считается по отпечатку значимых полей, а не по байтам исходного JSON:
// 1С может переставить ключи, и это не должно выглядеть как другой конверт.
func (e *Envelope) BodySHA256() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%d|%s|%s|%s|%s|%s|", e.Schema, e.ID, e.Kind, e.Stand.Key, e.Stand.Fingerprint, e.Stand.Platform))
	if e.MeasuredAtUTC != nil {
		b.WriteString(fmt.Sprintf("%d|", *e.MeasuredAtUTC))
	}
	if e.CapturedAtUTC != nil {
		b.WriteString(fmt.Sprintf("%d|", *e.CapturedAtUTC))
	}
	for _, r := range e.Results {
		b.WriteString(fmt.Sprintf("%s:%.6f:%.6f:%.6f:%.6f:%d;", r.Scenario, r.MedianMs, r.PerUnitMs, r.Portion, r.MinMs, r.Repeats))
	}
	if e.Passport != nil {
		b.WriteString(fmt.Sprintf("p%d:%d;", e.Passport.Schema, len(e.Passport.Properties)))
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// Normalize приводит необязательные поля к согласованному виду и возвращает
// список замечаний (не ошибок): они попадают в ingest_log.issue.
func (e *Envelope) Normalize() []string {
	var issues []string

	if e.Sig == "" {
		e.Sig = "none"
	}
	if e.Kind == "" {
		e.Kind = KindResult
	}

	// measured_at_utc: если не пришёл, выводим из measured_at.
	if e.MeasuredAtUTC == nil {
		if ts, ok := parseTime(e.MeasuredAt); ok {
			e.MeasuredAtUTC = &ts
			if !hasOffset(e.MeasuredAt) {
				issues = append(issues, "tz_missing")
			}
		}
	}
	if e.CapturedAtUTC == nil {
		if ts, ok := parseTime(e.CapturedAt); ok {
			e.CapturedAtUTC = &ts
		}
	}
	if e.MeasuredAtUTC != nil && e.MeasuredAt != "" {
		if ts, ok := parseTime(e.MeasuredAt); ok && abs64(ts-*e.MeasuredAtUTC) > 300 {
			issues = append(issues, "ts_mismatch")
		}
	}

	for i := range e.Results {
		r := &e.Results[i]
		if r.Unit == "" {
			r.Unit = ""
		}
		if r.Repeats == 0 {
			r.Repeats = 1
		}
		if r.PerUnitMs == 0 && r.MedianMs > 0 && r.Portion > 0 {
			r.PerUnitMs = r.MedianMs / r.Portion * 1000
			issues = append(issues, "per_unit_computed")
		} else if r.PerUnitMs > 0 && r.MedianMs > 0 && r.Portion > 0 {
			calc := r.MedianMs / r.Portion * 1000
			if calc > 0 && absFloat(r.PerUnitMs-calc)/calc > 0.005 {
				issues = append(issues, "per_unit_mismatch")
			}
		}
	}
	for i := range e.History {
		if e.History[i].MeasuredAtUTC == nil {
			e.History[i].MeasuredAtUTC = e.MeasuredAtUTC
		}
	}
	return issues
}

// Validate проверяет обязательные поля. Возвращает ErrUnsupportedSchema
// отдельно, чтобы приёмник мог отдать 415 вместо 400.
func (e *Envelope) Validate() error {
	if e.Schema != Schema {
		return fmt.Errorf("%w: %d", ErrUnsupportedSchema, e.Schema)
	}
	if !idRe.MatchString(e.ID) {
		return fmt.Errorf("%w: id %q не соответствует ^(r|p|h)-[A-Za-z0-9_-]{43}$", ErrInvalid, e.ID)
	}
	switch e.Kind {
	case KindResult, KindPassport, KindHistory:
	default:
		return fmt.Errorf("%w: kind %q", ErrInvalid, e.Kind)
	}
	if strings.TrimSpace(e.Stand.Key) == "" || len(e.Stand.Key) > 120 {
		return fmt.Errorf("%w: stand.key пуст или длиннее 120", ErrInvalid)
	}
	if !fingerprintRe.MatchString(e.Stand.Fingerprint) {
		return fmt.Errorf("%w: stand.fingerprint должен быть 64 hex", ErrInvalid)
	}

	switch e.Kind {
	case KindResult, KindHistory:
		items := e.Results
		if e.Kind == KindHistory {
			items = e.History
		}
		if len(items) == 0 {
			return fmt.Errorf("%w: нет замеров для вида %s", ErrInvalid, e.Kind)
		}
		if len(items) > 500 {
			return fmt.Errorf("%w: более 500 замеров в конверте", ErrInvalid)
		}
		if e.MeasuredAtUTC == nil {
			return fmt.Errorf("%w: нет measured_at_utc", ErrInvalid)
		}
		for _, r := range items {
			if err := validateResult(r); err != nil {
				return err
			}
		}
	case KindPassport:
		if e.Passport == nil {
			return fmt.Errorf("%w: нет passport", ErrInvalid)
		}
		if e.Passport.Schema < 1 || e.Passport.Schema > 3 {
			return fmt.Errorf("%w: passport.schema %d вне 1..3", ErrInvalid, e.Passport.Schema)
		}
		if e.CapturedAtUTC == nil {
			return fmt.Errorf("%w: нет captured_at_utc", ErrInvalid)
		}
	}
	return nil
}

func validateResult(r Result) error {
	if strings.TrimSpace(r.Scenario) == "" || len(r.Scenario) > 60 {
		return fmt.Errorf("%w: scenario пуст или длиннее 60", ErrInvalid)
	}
	if !(r.MedianMs > 0) || r.MedianMs > 3600000 {
		return fmt.Errorf("%w: median_ms=%v вне (0, 3600000]", ErrInvalid, r.MedianMs)
	}
	if !(r.Portion > 0) {
		return fmt.Errorf("%w: portion=%v должно быть > 0", ErrInvalid, r.Portion)
	}
	if r.Repeats < 1 || r.Repeats > 1000 {
		return fmt.Errorf("%w: repeats=%d вне 1..1000", ErrInvalid, r.Repeats)
	}
	return nil
}

func parseTime(s string) (int64, bool) {
	if strings.TrimSpace(s) == "" {
		return 0, false
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Unix(), true
		}
	}
	return 0, false
}

func hasOffset(s string) bool {
	// "+03:00" / "-05:00" / "Z"
	return strings.HasSuffix(s, "Z") || (len(s) >= 6 && (s[len(s)-6] == '+' || s[len(s)-6] == '-'))
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
