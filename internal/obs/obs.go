// Package obs: структурный журнал и простые метрики без внешних зависимостей.
package obs

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// NewLogger возвращает JSON-логгер в stderr: journald разбирает его сам.
func NewLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}

// Metrics - минимальный набор счётчиков в формате Prometheus.
// Полноценный клиент здесь не нужен: на 1 ядре важны числа, а не библиотека.
type Metrics struct {
	mu      sync.Mutex
	byCode  map[int]int64
	total   atomic.Int64
	started time.Time
	vecs    []*CounterVec
}

// CounterVec - счётчик с метками (канал, код и т.п.).
type CounterVec struct {
	mu   sync.Mutex
	name string
	help string
	vals map[string]int64
	keys []string
}

// CounterVec создаёт (и регистрирует для выгрузки) именованный счётчик с метками.
func (m *Metrics) CounterVec(name, help string) *CounterVec {
	c := &CounterVec{name: name, help: help, vals: make(map[string]int64)}
	m.mu.Lock()
	m.vecs = append(m.vecs, c)
	m.mu.Unlock()
	return c
}

// WithLabelValues возвращает дескриптор для увеличения.
func (c *CounterVec) WithLabelValues(labels ...string) *counterHandle {
	key := strings.Join(labels, "|")
	c.mu.Lock()
	if _, ok := c.vals[key]; !ok {
		c.keys = append(c.keys, key)
	}
	c.mu.Unlock()
	return &counterHandle{vec: c, key: key}
}

type counterHandle struct {
	vec *CounterVec
	key string
}

// Inc увеличивает значение на единицу.
func (h *counterHandle) Inc() {
	h.vec.mu.Lock()
	h.vec.vals[h.key]++
	h.vec.mu.Unlock()
}

// NewMetrics создаёт счётчики с отметкой времени старта.
func NewMetrics() *Metrics {
	return &Metrics{byCode: make(map[int]int64), started: time.Now()}
}

// Observe учитывает один ответ с указанным кодом.
func (m *Metrics) Observe(code int) {
	m.total.Add(1)
	m.mu.Lock()
	m.byCode[code]++
	m.mu.Unlock()
}

// Handler отдаёт текстовую выгрузку метрик.
func (m *Metrics) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		codes := make([]int, 0, len(m.byCode))
		for code := range m.byCode {
			codes = append(codes, code)
		}
		sort.Ints(codes)

		var b strings.Builder
		b.WriteString("# HELP perfd_http_requests_total Всего HTTP-запросов\n")
		b.WriteString("# TYPE perfd_http_requests_total counter\n")
		fmt.Fprintf(&b, "perfd_http_requests_total %d\n", m.total.Load())
		b.WriteString("# HELP perfd_http_responses_total Ответов по коду состояния\n")
		b.WriteString("# TYPE perfd_http_responses_total counter\n")
		for _, code := range codes {
			fmt.Fprintf(&b, "perfd_http_responses_total{code=\"%d\"} %d\n", code, m.byCode[code])
		}
		b.WriteString("# HELP perfd_uptime_seconds Время с запуска процесса\n")
		b.WriteString("# TYPE perfd_uptime_seconds gauge\n")
		fmt.Fprintf(&b, "perfd_uptime_seconds %.0f\n", time.Since(m.started).Seconds())

		for _, vec := range m.vecs {
			vec.mu.Lock()
			fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s counter\n", vec.name, vec.help, vec.name)
			for _, key := range vec.keys {
				fmt.Fprintf(&b, "%s{result=\"%s\"} %d\n", vec.name, key, vec.vals[key])
			}
			vec.mu.Unlock()
		}
		m.mu.Unlock()

		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(b.String()))
	}
}

// Middleware считает запросы и пишет строку журнала на каждый вызов.
func Middleware(log *slog.Logger, m *Metrics, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		m.Observe(sw.status)
		log.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"dur_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
		)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status  int
	written bool
}

func (s *statusWriter) WriteHeader(code int) {
	if s.written {
		return
	}
	s.status = code
	s.written = true
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(p []byte) (int, error) {
	if !s.written {
		s.written = true
	}
	return s.ResponseWriter.Write(p)
}
