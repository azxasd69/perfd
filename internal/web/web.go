// Package web: маршрутизация, шаблоны и статика портала.
// Реализует ТЗ § 6–7: приём конвертов, API чтения, страницы витрины.
package web

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"perfd/internal/obs"
	"perfd/internal/store"
)

// Version подставляется при сборке: -ldflags "-X perfd/internal/web.Version=<hash>".
var Version = "dev"

//go:embed templates/*.html
var templateFS embed.FS

//go:embed assets/*
var assetFS embed.FS

// Options — параметры, которые приходят из конфигурации процесса.
type Options struct {
	Base             string   // префикс публикации: "" или "/perf"
	MaxBody          int64
	Schema           int
	Formula          int
	DefaultBase      string
	IngestTokenSHA256 string
	Channels         []string
	StatusHost       string
}

// Server держит зависимости обработчиков.
type Server struct {
	store   *store.Store
	log     *slog.Logger
	metrics *obs.Metrics
	opts    Options
	started time.Time
	tpl     *template.Template
	assets  http.Handler
	ingest  *obs.CounterVec
}

// New собирает сервер и разбирает встроенные шаблоны и статику.
func New(st *store.Store, log *slog.Logger, m *obs.Metrics, opts Options) (*Server, error) {
	tpl, err := template.New("root").Funcs(template.FuncMap{
		"fmtTime":    fmtTime,
		"fmtNum":     fmtNum,
		"fmtDelta":   fmtDelta,
		"deltaClass": deltaClass,
		"humanBytes": humanBytes,
	}).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	sub, err := fs.Sub(assetFS, "assets")
	if err != nil {
		return nil, err
	}
	if len(opts.Channels) == 0 {
		opts.Channels = []string{"http", "webdav"}
	}
	return &Server{
		store:   st,
		log:     log,
		metrics: m,
		opts:    opts,
		started: time.Now(),
		tpl:     tpl,
		assets:  http.FileServer(http.FS(sub)),
		ingest:  m.CounterVec("perfd_ingest_total", "Принятые конверты по каналу и результату"),
	}, nil
}

// Handler возвращает маршрутизатор.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	base := s.opts.Base

	// Служебные.
	mux.HandleFunc("GET "+base+"/healthz", s.handleHealth)
	mux.HandleFunc("GET "+base+"/readyz", s.handleReady)
	mux.HandleFunc("GET "+base+"/healthz/channels", s.handleChannels)
	mux.HandleFunc("GET "+base+"/metrics", s.metrics.Handler())

	// Приём.
	mux.HandleFunc("POST "+base+"/api/v1/ingest", s.handleIngest)
	mux.HandleFunc("POST "+base+"/api/v1/import", s.handleImport)

	// Чтение API.
	mux.HandleFunc("GET "+base+"/api/v1/meta", s.handleMeta)
	mux.HandleFunc("GET "+base+"/api/v1/status", s.handleStatus)
	mux.HandleFunc("GET "+base+"/api/v1/stands", s.apiListStands)
	mux.HandleFunc("GET "+base+"/api/v1/stands/{slug}", s.apiStand)
	mux.HandleFunc("GET "+base+"/api/v1/stands/{slug}/results", s.apiStandResults)
	mux.HandleFunc("GET "+base+"/api/v1/stands/{slug}/history", s.apiStandHistory)
	mux.HandleFunc("GET "+base+"/api/v1/top", s.apiTop)
	mux.HandleFunc("GET "+base+"/api/v1/ratings", s.apiRatings)
	mux.HandleFunc("GET "+base+"/api/v1/compare", s.apiCompare)

	// Статика и страницы.
	mux.Handle("GET "+base+"/assets/", http.StripPrefix(base+"/assets/", cacheForever(s.assets)))
	mux.HandleFunc("GET "+base+"/stands", s.handleStands)
	mux.HandleFunc("GET "+base+"/stands/{slug}", s.handleStand)
	mux.HandleFunc("GET "+base+"/stands/{slug}/history/{scenario}", s.handleHistoryPage)
	mux.HandleFunc("GET "+base+"/top", s.handleTop)
	mux.HandleFunc("GET "+base+"/about", s.handleAbout)
	mux.HandleFunc("GET "+base+"/{$}", s.handleDashboard)

	// Канал 4 (HTTP 80) данные не принимает — решение Р-21.
	mux.HandleFunc("POST "+base+"/ingest-plain", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": "channel_closed", "message": "приём по HTTP 80 выключен (О-05, Р-21)",
		})
	})

	// Если портал живёт под префиксом, корень хоста уводим на портал.
	if base != "" {
		prefix := base
		mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, prefix+"/", http.StatusFound)
		})
	}
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	noStore(w)
	if err := s.store.Ping(ctx); err != nil {
		s.log.Error("healthz: база недоступна", "err", err)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("db unavailable\n"))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	schemaVersion, err := s.store.SchemaVersion(r.Context())
	if err != nil {
		schemaVersion = 0
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"app":          "PerfTest portal",
		"service":      "perfd",
		"version":      Version,
		"schemas":      []int{s.opts.Schema},
		"kinds":        []string{"result", "passport", "history"},
		"formula":      s.opts.Formula,
		"default_base": s.opts.DefaultBase,
		"base_path":    s.opts.Base,
		"enc":          "unsupported",
		"sig":          map[string]any{"supported": []string{"none", "hmac-sha256"}, "required": false},
		"channels": map[string]any{
			"enabled":      s.opts.Channels,
			"data":         []string{"http", "webdav"},
			"closed":       []string{"http80"},
		},
		"limits": map[string]any{
			"body_bytes":  s.opts.MaxBody,
			"batch_items": maxBatchItems,
			"file_bytes":  5242880,
		},
		"db_schema":   schemaVersion,
		"server_time_utc": time.Now().UTC().Format(time.RFC3339),
	})
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	noStore(w)
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
}

func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}

func cacheForever(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".css") || strings.HasSuffix(r.URL.Path, ".js") || strings.HasSuffix(r.URL.Path, ".svg") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		next.ServeHTTP(w, r)
	})
}

// renderTemplate отдаёт готовую страницу в буфере: ошибка шаблона не даёт
// полусобранную страницу с кодом 200.
func renderTemplate(tpl *template.Template, name string, data any) ([]byte, error) {
	var buf bytes.Buffer
	if err := tpl.ExecuteTemplate(&buf, name, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func ctxWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
