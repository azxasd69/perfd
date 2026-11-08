package web

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"perfd/internal/store"
)

// ---------- вспомогательное ----------

func randomID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b)
}

// diskFree возвращает свободное и общее место на файловой системе пути.
// Реализация — в pages_free_unix.go и pages_free_windows.go (build tags).

func fmtTime(unix int64) string {
	if unix == 0 {
		return "—"
	}
	return time.Unix(unix, 0).UTC().Format("02.01.2006 15:04")
}

func fmtNum(v float64, digits int) string {
	return strconv.FormatFloat(v, 'f', digits, 64)
}

func fmtDelta(p *float64) string {
	if p == nil {
		return "—"
	}
	return fmt.Sprintf("%+.1f %%", *p)
}

func deltaClass(p *float64) string {
	switch {
	case p == nil:
		return "muted"
	case *p > 1:
		return "good"
	case *p < -1:
		return "bad"
	default:
		return "neutral"
	}
}

func humanBytes(v uint64) string {
	const unit = 1024
	if v < unit {
		return fmt.Sprintf("%d Б", v)
	}
	div, exp := uint64(unit), 0
	for n := v / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %sБ", float64(v)/float64(div), []string{"К", "М", "Г", "Т"}[exp])
}

// ---------- модель страниц ----------

type pageData struct {
	Title       string
	Base        string
	Version     string
	Schema      int
	Formula     int
	DefaultBase string
	DBPath      string
	DBSchema    int
	Counts      store.Counts
	Started     string
	Uptime      string

	Stands      []store.StandRow
	Stand       store.StandRow
	Scenarios   []store.ScenarioRow
	Generations []store.GenerationRow
	Passport    string
	History     []store.HistoryPoint
	Top         []store.TopRow
	Scenario    string
	Granularity string
	Error       string
	ReadOnly    bool
}

func (s *Server) baseData(title string) pageData {
	ctx, cancel := ctxWithTimeout(3 * time.Second)
	defer cancel()
	counts, _ := s.store.Counts(ctx)
	dbSchema, _ := s.store.SchemaVersion(ctx)
	return pageData{
		Title:       title,
		Base:        s.opts.Base,
		Version:     Version,
		Schema:      s.opts.Schema,
		Formula:     s.opts.Formula,
		DefaultBase: s.opts.DefaultBase,
		DBPath:      s.store.Path(),
		DBSchema:    dbSchema,
		Counts:      counts,
		Started:     s.started.Format("02.01.2006 15:04:05"),
		Uptime:      time.Since(s.started).Round(time.Second).String(),
	}
}

func (s *Server) render(w http.ResponseWriter, name string, data pageData) {
	body, err := renderTemplate(s.tpl, name, data)
	if err != nil {
		s.log.Error("шаблон не отрисован", "tpl", name, "err", err)
		http.Error(w, "внутренняя ошибка", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	noStore(w)
	_, _ = w.Write(body)
}

// ---------- страницы ----------

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != s.opts.Base+"/" && r.URL.Path != s.opts.Base {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := ctxWithTimeout(3 * time.Second)
	defer cancel()

	data := s.baseData("PerfTest — стенды и замеры")
	stands, err := s.store.ListStands(ctx, s.hasReadAccess(r))
	if err != nil {
		s.log.Error("дашборд: список стендов", "err", err)
	}
	data.Stands = stands
	data.ReadOnly = !s.hasReadAccess(r)
	s.render(w, "dashboard", data)
}

func (s *Server) handleStands(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxWithTimeout(3 * time.Second)
	defer cancel()

	data := s.baseData("Стенды — PerfTest")
	stands, err := s.store.ListStands(ctx, s.hasReadAccess(r))
	if err != nil {
		s.log.Error("список стендов", "err", err)
	}
	data.Stands = stands
	data.ReadOnly = !s.hasReadAccess(r)
	s.render(w, "stands", data)
}

func (s *Server) handleStand(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	ctx, cancel := ctxWithTimeout(5 * time.Second)
	defer cancel()

	access := s.hasReadAccess(r)
	stand, err := s.store.Stand(ctx, slug, access)
	if err != nil {
		data := s.baseData("Стенд не найден — PerfTest")
		data.Error = "Стенд не найден или скрыт"
		w.WriteHeader(http.StatusNotFound)
		s.render(w, "stand", data)
		return
	}

	data := s.baseData(stand.Name + " — PerfTest")
	data.Stand = stand
	data.ReadOnly = !access

	if scenarios, err := s.store.Scenarios(ctx, stand.GenerationID); err == nil {
		data.Scenarios = scenarios
	} else {
		s.log.Error("сценарии стенда", "err", err)
	}
	if gens, err := s.store.Generations(ctx, slug); err == nil {
		data.Generations = gens
	}
	if props, _, _, err := s.store.Passport(ctx, stand.GenerationID); err == nil {
		data.Passport = props
	}
	s.render(w, "stand", data)
}

func (s *Server) handleHistoryPage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	scenario := r.PathValue("scenario")
	ctx, cancel := ctxWithTimeout(5 * time.Second)
	defer cancel()

	access := s.hasReadAccess(r)
	stand, err := s.store.Stand(ctx, slug, access)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		data := s.baseData("Стенд не найден — PerfTest")
		data.Error = "Стенд не найден или скрыт"
		s.render(w, "stand", data)
		return
	}

	data := s.baseData("История " + scenario + " — PerfTest")
	data.Stand = stand
	data.Scenario = scenario
	data.Granularity = r.URL.Query().Get("granularity")

	points, err := s.store.HistoryDay(ctx, stand.GenerationID, scenario, 0, 0, 200)
	if err != nil {
		s.log.Error("история", "err", err)
	}
	if data.Granularity == "month" {
		if monthly, err := s.store.HistoryMonth(ctx, stand.GenerationID, scenario); err == nil && len(monthly) > 0 {
			points = monthly
			data.Granularity = "month"
		}
	}
	if data.Granularity == "" {
		data.Granularity = "day"
	}
	data.History = points
	s.render(w, "history", data)
}

func (s *Server) handleTop(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxWithTimeout(5 * time.Second)
	defer cancel()

	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 3650 {
			days = n
		}
	}
	data := s.baseData("Топ-100 худших сценариев — PerfTest")
	rows, err := s.store.Top(ctx, days, 100, s.hasReadAccess(r))
	if err != nil {
		s.log.Error("топ худших", "err", err)
	}
	data.Top = rows
	s.render(w, "top", data)
}

func (s *Server) handleAbout(w http.ResponseWriter, r *http.Request) {
	data := s.baseData("Методика — PerfTest")
	s.render(w, "about", data)
}

// ---------- API чтения ----------

func (s *Server) apiListStands(w http.ResponseWriter, r *http.Request) {
	stands, err := s.store.ListStands(r.Context(), s.hasReadAccess(r))
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "db_unavailable"})
		return
	}
	out := make([]map[string]any, 0, len(stands))
	for _, st := range stands {
		out = append(out, map[string]any{
			"slug": st.Slug, "name": st.Name, "platform": st.Platform, "os": st.OS, "cpu": st.CPU,
			"index": st.Index, "scenarios_used": st.ScenariosUsed, "low_data": st.LowData,
			"last_measured_at": isoOrNil(st.LastMeasuredAt), "passport_missing": st.PassportMissing,
			"visibility": st.Visibility, "generation_id": st.GenerationID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"stands": out})
}

func (s *Server) apiStand(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	access := s.hasReadAccess(r)
	stand, err := s.store.Stand(r.Context(), slug, access)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found", "message": "стенд не найден или скрыт"})
		return
	}

	scenarios, _ := s.store.Scenarios(r.Context(), stand.GenerationID)
	gc := make([]map[string]any, 0, len(scenarios))
	for _, sc := range scenarios {
		gc = append(gc, map[string]any{
			"scenario": sc.Code, "representation": sc.Representation, "median_ms": sc.MedianMs,
			"per_unit_ms": sc.PerUnitMs, "unit": sc.Unit, "portion": sc.Portion, "repeats": sc.Repeats,
			"min_ms": sc.MinMs, "max_ms": sc.MaxMs, "delta_pct": sc.DeltaPct,
			"measured_at": isoOrNil(sc.MeasuredAt),
		})
	}

	var passport any
	if props, schema, captured, err := s.store.Passport(r.Context(), stand.GenerationID); err == nil {
		passport = map[string]any{"schema": schema, "captured_at": isoOrNil(captured), "properties": rawJSON(props)}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"slug": stand.Slug, "name": stand.Name, "platform": stand.Platform, "os": stand.OS, "cpu": stand.CPU,
		"visibility": stand.Visibility, "generation_id": stand.GenerationID, "passport_missing": stand.PassportMissing,
		"index": map[string]any{
			"value": stand.Index, "base": s.opts.DefaultBase, "formula": s.opts.Formula,
			"scenarios_used": stand.ScenariosUsed, "low_data": stand.LowData,
			"best":  map[string]any{"scenario": stand.BestScenario, "delta_pct": stand.BestDeltaPct},
			"worst": map[string]any{"scenario": stand.WorstScenario, "delta_pct": stand.WorstDeltaPct},
			"last_measured_at": isoOrNil(stand.LastMeasuredAt),
		},
		"scenarios": gc,
		"passport":  passport,
	})
}

func (s *Server) apiStandResults(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	stand, err := s.store.Stand(r.Context(), slug, s.hasReadAccess(r))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
		return
	}
	scenarios, err := s.store.Scenarios(r.Context(), stand.GenerationID)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "db_unavailable"})
		return
	}
	out := make([]map[string]any, 0, len(scenarios))
	for _, sc := range scenarios {
		out = append(out, map[string]any{
			"scenario": sc.Code, "median_ms": sc.MedianMs, "per_unit_ms": sc.PerUnitMs,
			"unit": sc.Unit, "portion": sc.Portion, "repeats": sc.Repeats,
			"min_ms": sc.MinMs, "max_ms": sc.MaxMs, "delta_pct": sc.DeltaPct, "measured_at": isoOrNil(sc.MeasuredAt),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"slug": slug, "results": out, "count": len(out)})
}

func (s *Server) apiStandHistory(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	scenario := r.URL.Query().Get("scenario")
	if scenario == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing_field", "message": "нужен параметр scenario"})
		return
	}
	stand, err := s.store.Stand(r.Context(), slug, s.hasReadAccess(r))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
		return
	}

	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	granularity := r.URL.Query().Get("granularity")

	points, err := s.store.HistoryDay(r.Context(), stand.GenerationID, scenario, 0, 0, limit)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "db_unavailable"})
		return
	}
	if granularity == "month" {
		if monthly, err := s.store.HistoryMonth(r.Context(), stand.GenerationID, scenario); err == nil && len(monthly) > 0 {
			points, granularity = monthly, "month"
		}
	}
	if granularity == "" {
		granularity = "day"
	}

	out := make([]map[string]any, 0, len(points))
	for _, p := range points {
		out = append(out, map[string]any{
			"measured_at_utc": p.MeasuredAt, "median_ms": p.MedianMs, "per_unit_ms": p.PerUnitMs,
			"min_ms": p.MinMs, "max_ms": p.MaxMs, "samples": p.Samples,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"slug": slug, "scenario": scenario, "granularity": granularity, "points": out, "count": len(out),
	})
}

func (s *Server) apiTop(w http.ResponseWriter, r *http.Request) {
	days, limit := 30, 100
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			days = n
		}
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	rows, err := s.store.Top(r.Context(), days, limit, s.hasReadAccess(r))
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "db_unavailable"})
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, t := range rows {
		out = append(out, map[string]any{
			"slug": t.Slug, "scenario": t.Scenario, "platform": t.Platform,
			"per_unit_ms": t.PerUnitMs, "median_ms": t.MedianMs, "measured_at_utc": t.MeasuredAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": days, "items": out, "count": len(out)})
}

func (s *Server) apiRatings(w http.ResponseWriter, r *http.Request) {
	stands, err := s.store.ListStands(r.Context(), s.hasReadAccess(r))
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "db_unavailable"})
		return
	}
	out := make([]map[string]any, 0, len(stands))
	for _, st := range stands {
		out = append(out, map[string]any{
			"slug": st.Slug, "index": st.Index, "scenarios_used": st.ScenariosUsed,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"base": s.opts.DefaultBase, "formula": s.opts.Formula, "items": out})
}

func (s *Server) apiCompare(w http.ResponseWriter, r *http.Request) {
	a, b := r.URL.Query().Get("a"), r.URL.Query().Get("b")
	if a == "" || b == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing_field", "message": "нужны a и b"})
		return
	}
	rows, common, missing, err := s.store.Compare(r.Context(), a, b)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found", "message": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, c := range rows {
		out = append(out, map[string]any{
			"scenario": c.Scenario, "a_per_unit_ms": c.APerUnitMs, "b_per_unit_ms": c.BPerUnitMs, "delta_pct": c.DeltaPct,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"a": a, "b": b, "scenarios": out, "summary": map[string]any{"common": common, "missing": missing},
	})
}

// ---------- мелочи ----------

func isoOrNil(unix int64) any {
	if unix == 0 {
		return nil
	}
	return time.Unix(unix, 0).UTC().Format(time.RFC3339)
}

// rawJSON отдаёт паспорт как есть, не превращая его в строку.
type rawJSON string

func (r rawJSON) MarshalJSON() ([]byte, error) {
	if strings.TrimSpace(string(r)) == "" || !strings.HasPrefix(strings.TrimSpace(string(r)), "{") {
		return []byte("null"), nil
	}
	return []byte(r), nil
}

var _ = os.Getpid
