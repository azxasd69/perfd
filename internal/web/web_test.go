package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"perfd/internal/obs"
	"perfd/internal/store"
)

func newServer(t *testing.T, base string) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "web.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	srv, err := New(st, log, obs.NewMetrics(), Options{
		Base:        base,
		MaxBody:     262144,
		Schema:      1,
		Formula:     1,
		DefaultBase: "ven-8327",
	})
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}
	return srv, st
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestMeta(t *testing.T) {
	srv, _ := newServer(t, "/perf")
	rec := get(t, srv.Handler(), "/perf/api/v1/meta")

	if rec.Code != http.StatusOK {
		t.Fatalf("код %d, ожидался 200; тело: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Service     string `json:"service"`
		Schemas     []int  `json:"schemas"`
		Formula     int    `json:"formula"`
		DefaultBase string `json:"default_base"`
		BasePath    string `json:"base_path"`
		DBSchema    int    `json:"db_schema"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	if body.Service != "perfd" || len(body.Schemas) != 1 || body.Schemas[0] != 1 {
		t.Fatalf("неожиданный /meta: %+v", body)
	}
	if body.Formula != 1 || body.BasePath != "/perf" || body.DBSchema < 2 {
		t.Fatalf("поля /meta не совпадают с конфигурацией: %+v", body)
	}
}

func TestHealthz(t *testing.T) {
	srv, st := newServer(t, "/perf")
	rec := get(t, srv.Handler(), "/perf/healthz")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "ok" {
		t.Fatalf("healthz: код %d, тело %q", rec.Code, rec.Body.String())
	}

	// Закрытая база должна давать 503, а не 200: на этом держится мониторинг.
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	rec = get(t, srv.Handler(), "/perf/healthz")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("при закрытой базе ожидался 503, получено %d", rec.Code)
	}
}

func TestMetrics(t *testing.T) {
	srv, _ := newServer(t, "/perf")
	rec := get(t, srv.Handler(), "/perf/metrics")
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "perfd_http_requests_total") {
		t.Fatalf("в метриках нет ожидаемого счётчика: %s", rec.Body.String())
	}
}

func TestDashboard(t *testing.T) {
	srv, _ := newServer(t, "/perf")
	rec := get(t, srv.Handler(), "/perf/")
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d", rec.Code)
	}
	html := rec.Body.String()
	if !strings.Contains(html, "<html") || !strings.Contains(html, "Стенды и замеры") {
		t.Fatalf("страница не отрисована: %s", html[:min(200, len(html))])
	}
	if !strings.Contains(html, "/perf/assets/app.css") {
		t.Fatalf("ссылка на статику не учитывает префикс публикации")
	}
}

func TestUnknownPathUnderBaseIs404(t *testing.T) {
	srv, _ := newServer(t, "/perf")
	rec := get(t, srv.Handler(), "/perf/no-such-page")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("неизвестный путь под префиксом должен давать 404, получено %d", rec.Code)
	}
	// Список стендов теперь маршрут, а не 404 (ТЗ ФТ-17).
	rec = get(t, srv.Handler(), "/perf/stands")
	if rec.Code != http.StatusOK {
		t.Fatalf("страница стендов должна отдавать 200, получено %d", rec.Code)
	}
}

func TestRootRedirectsToBase(t *testing.T) {
	srv, _ := newServer(t, "/perf")
	rec := get(t, srv.Handler(), "/")
	if rec.Code != http.StatusFound {
		t.Fatalf("ожидался 302, получено %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/perf/" {
		t.Fatalf("Location %q, ожидался /perf/", loc)
	}
}

func TestNoBaseServesRoot(t *testing.T) {
	srv, _ := newServer(t, "")
	rec := get(t, srv.Handler(), "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("без префикса корень должен отдавать страницу, код %d", rec.Code)
	}
}
