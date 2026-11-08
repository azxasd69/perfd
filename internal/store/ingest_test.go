package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"perfd/internal/model"
)

func openIngest(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "ingest.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return st
}

func envelope(kind, id, key, fingerprint string, scenario string, median, portion float64) model.Envelope {
	ts := time.Now().Unix()
	e := model.Envelope{
		Schema: model.Schema, ID: id, Kind: kind,
		Stand: model.Stand{Key: key, Fingerprint: fingerprint, Platform: "8.3.27.2342"},
	}
	if kind == model.KindPassport {
		e.CapturedAtUTC = &ts
		e.Passport = &model.Passport{Schema: 3, Properties: map[string]any{
			"СерверКомпьютер": "TEST-PC", "СерверОС": "Ubuntu 24.04",
			"СерверЦПУ": "Xeon", "СерверЦПУПотоки": float64(8), "СерверПамятьМб": float64(16384),
			"РежимИБ": "Файловый", "СУБД": "Файловая",
			"СтрокаСоединенияИБ": `File="d:\base";Pwd=secret`,
		}}
		return e
	}
	e.MeasuredAtUTC = &ts
	e.Results = []model.Result{{
		Scenario: scenario, MedianMs: median, PerUnitMs: median / portion * 1000,
		Unit: "итераций", Portion: portion, Repeats: 2, MinMs: median, MaxMs: median,
	}}
	return e
}

func ingest(t *testing.T, st *Store, e model.Envelope) EnvelopeOutcome {
	t.Helper()
	out, err := st.Ingest(context.Background(), EnvelopeInput{Envelope: e, Channel: "http", Formula: 1})
	if err != nil {
		t.Fatalf("Ingest(%s): %v", e.ID, err)
	}
	return out
}

func id(prefix string, fill string) string {
	s := fill
	for len(s) < 43 {
		s += fill
	}
	return prefix + "-" + s[:43]
}

// Т-02: повтор конверта не создаёт дубль.
func TestIngestIdempotent(t *testing.T) {
	st := openIngest(t)
	e := envelope(model.KindResult, id("r", "a"), "PC-1", fingerprintFor("f1"), "CPU_Арифметика", 100, 1000)

	first := ingest(t, st, e)
	if first.Status != StatusOK {
		t.Fatalf("первый конверт: статус %s (%s)", first.Status, first.Message)
	}
	second := ingest(t, st, e)
	if second.Status != StatusDuplicate {
		t.Fatalf("повтор должен быть duplicate, получено %s", second.Status)
	}

	counts, err := st.Counts(context.Background())
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.Results != 1 || counts.History != 1 || counts.Ingest != 1 {
		t.Fatalf("дубль попал в базу: %+v", counts)
	}
}

// Конфликт: тот же id с другим телом — 409 и запись не меняется.
func TestIngestConflict(t *testing.T) {
	st := openIngest(t)
	e1 := envelope(model.KindResult, id("r", "b"), "PC-1", fingerprintFor("f1"), "CPU_Арифметика", 100, 1000)
	e2 := envelope(model.KindResult, id("r", "b"), "PC-1", fingerprintFor("f1"), "CPU_Арифметика", 200, 1000)
	e2.MeasuredAtUTC = e1.MeasuredAtUTC

	if out := ingest(t, st, e1); out.Status != StatusOK {
		t.Fatalf("первый: %s", out.Status)
	}
	out := ingest(t, st, e2)
	if out.Status != StatusConflict || out.Code != 409 {
		t.Fatalf("ожидался conflict/409, получено %s/%d", out.Status, out.Code)
	}
}

// К-11: индекс ИИ считается как в 1С — 100 × среднее(база/стенд), округление до 1 знака.
func TestAggregateIndexMatches1C(t *testing.T) {
	st := openIngest(t)
	ctx := context.Background()

	baseFP, standFP := fingerprintFor("base"), fingerprintFor("stand")
	for i, sc := range []struct {
		code          string
		basePer, stPer float64
	}{
		{"CPU_Арифметика", 100, 200}, // отношение 0.5
		{"CPU_Строки", 100, 100},     // 1.0
		{"CPU_Коллекции", 100, 125},  // 0.8
	} {
		be := envelope(model.KindResult, id("r", string(rune('c'+i))), "ВЕНДОР-8327", baseFP, sc.code, sc.basePer, 1000)
		be.Results[0].PerUnitMs = sc.basePer
		if out := ingest(t, st, be); out.Status != StatusOK {
			t.Fatalf("база %s: %s", sc.code, out.Status)
		}
		se := envelope(model.KindResult, id("r", string(rune('x'+i))), "PC-STAND", standFP, sc.code, sc.stPer, 1000)
		se.Results[0].PerUnitMs = sc.stPer
		if out := ingest(t, st, se); out.Status != StatusOK {
			t.Fatalf("стенд %s: %s", sc.code, out.Status)
		}
	}

	// (0.5 + 1.0 + 0.8) / 3 × 100 = 76.666… → 76.7
	var index float64
	var used int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT a.index_value, a.scenarios_used FROM aggregates a
		  JOIN stand_generations g ON g.id = a.generation_id
		 WHERE g.stand_key = 'PC-STAND' ORDER BY a.computed_at DESC LIMIT 1`).Scan(&index, &used); err != nil {
		t.Fatalf("чтение агрегата: %v", err)
	}
	if used != 3 {
		t.Fatalf("парных сценариев %d, ожидалось 3", used)
	}
	if diff := index - 76.7; diff > 0.001 || diff < -0.001 {
		t.Fatalf("индекс %v, ожидалось 76.7", index)
	}
}

// Р-10: смена отпечатка создаёт новое поколение, старые замеры не смешиваются.
func TestGenerationOnFingerprintChange(t *testing.T) {
	st := openIngest(t)
	ctx := context.Background()

	e1 := envelope(model.KindResult, id("r", "g"), "PC-2", fingerprintFor("gen1"), "CPU_Арифметика", 100, 1000)
	if out := ingest(t, st, e1); out.Status != StatusOK {
		t.Fatalf("первое поколение: %s", out.Status)
	}
	e2 := envelope(model.KindResult, id("r", "h"), "PC-2", fingerprintFor("gen2"), "CPU_Арифметика", 90, 1000)
	out := ingest(t, st, e2)
	if out.Status != StatusOK {
		t.Fatalf("второе поколение: %s", out.Status)
	}
	if out.GenerationID == 1 {
		t.Fatalf("новый отпечаток должен дать новое поколение")
	}

	var gens int
	if err := st.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM stand_generations WHERE stand_id = 'PC-2'").Scan(&gens); err != nil {
		t.Fatalf("поколения: %v", err)
	}
	if gens != 2 {
		t.Fatalf("поколений %d, ожидалось 2", gens)
	}
}

// Паспорт: пароль вырезается, поля извлекаются, passport_missing снимается.
func TestPassportMaskAndExtract(t *testing.T) {
	st := openIngest(t)
	ctx := context.Background()

	e := envelope(model.KindPassport, id("p", "z"), "PC-3", fingerprintFor("pass"), "", 0, 0)
	out := ingest(t, st, e)
	if out.Status != StatusOK {
		t.Fatalf("паспорт: %s (%s)", out.Status, out.Message)
	}
	if out.Issue != "password_in_fields" {
		t.Fatalf("ожидалось замечание password_in_fields, получено %q", out.Issue)
	}

	props, schema, _, err := st.Passport(ctx, out.GenerationID)
	if err != nil {
		t.Fatalf("Passport: %v", err)
	}
	if schema != 3 {
		t.Fatalf("схема паспорта %d", schema)
	}
	if !containsPwdMask(props) {
		t.Fatalf("пароль не замаскирован: %s", props)
	}
	if containsSecret(props) {
		t.Fatalf("в паспорте остался исходный пароль: %s", props)
	}

	var oscpu string
	var missing int
	if err := st.DB().QueryRowContext(ctx,
		"SELECT os, passport_missing FROM stand_generations WHERE id = ?", out.GenerationID).Scan(&oscpu, &missing); err != nil {
		t.Fatalf("поколение: %v", err)
	}
	if oscpu != "Ubuntu 24.04" || missing != 0 {
		t.Fatalf("поля паспорта не извлеклись: os=%q missing=%d", oscpu, missing)
	}
}

// Свёртка: месяц старше окна переносится в history_monthly и только затем удаляется.
func TestRetentionRollsUpBeforeDelete(t *testing.T) {
	st := openIngest(t)
	ctx := context.Background()

	e := envelope(model.KindResult, id("r", "k"), "PC-4", fingerprintFor("ret"), "CPU_Арифметика", 100, 1000)
	out := ingest(t, st, e)
	if out.Status != StatusOK {
		t.Fatalf("приём: %s", out.Status)
	}

	old := time.Now().AddDate(-2, 0, 0).Unix()
	if _, err := st.DB().ExecContext(ctx,
		"UPDATE history SET measured_at = ? WHERE generation_id = ?", old, out.GenerationID); err != nil {
		t.Fatalf("сдвиг даты: %v", err)
	}

	res, err := st.Retention(ctx, 12, 90)
	if err != nil {
		t.Fatalf("Retention: %v", err)
	}
	if res.RolledMonths != 1 || res.DeletedRows != 1 {
		t.Fatalf("ожидалась 1 свёртка и 1 удаление, получено %+v", res)
	}

	var monthly int64
	if err := st.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM history_monthly WHERE generation_id = ?", out.GenerationID).Scan(&monthly); err != nil {
		t.Fatalf("свёртки: %v", err)
	}
	if monthly != 1 {
		t.Fatalf("свёрток %d, ожидалась 1", monthly)
	}
}

func fingerprintFor(seed string) string {
	out := ""
	for _, ch := range seed {
		out += string("0123456789abcdef"[int(ch)%16])
	}
	for len(out) < 64 {
		out += "0"
	}
	return out[:64]
}

func containsPwdMask(s string) bool { return len(s) > 0 && (indexOf(s, "Pwd=***") >= 0) }
func containsSecret(s string) bool  { return indexOf(s, "Pwd=secret") >= 0 }

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
