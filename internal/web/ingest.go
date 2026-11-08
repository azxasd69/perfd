package web

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"perfd/internal/model"
	"perfd/internal/store"
)

// maxBatchItems — предел батча по ТЗ § 7.7.
const maxBatchItems = 50

// authRole проверяет токен и возвращает роль: ingest | read | admin.
// Токен приходит только в заголовке (ТЗ § 7.2); в URL и логах его нет.
func (s *Server) authRole(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", false
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	token := strings.TrimSpace(h[len(prefix):])
	if token == "" {
		return "", false
	}
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])

	// 1. Токены из таблицы (ротация, роли, отзыв).
	if rows, err := s.store.DB().QueryContext(r.Context(),
		"SELECT role FROM tokens WHERE token_hash = ? AND revoked_at IS NULL", hash); err == nil {
		defer func() { _ = rows.Close() }()
		if rows.Next() {
			var role string
			if err := rows.Scan(&role); err == nil {
				return role, true
			}
		}
	}

	// 2. Токен из окружения — один на роль ingest (обратная совместимость).
	if s.opts.IngestTokenSHA256 != "" &&
		subtle.ConstantTimeCompare([]byte(hash), []byte(s.opts.IngestTokenSHA256)) == 1 {
		return "ingest", true
	}
	return "", false
}

func (s *Server) hasReadAccess(r *http.Request) bool {
	role, ok := s.authRole(r)
	return ok && (role == "read" || role == "admin" || role == "ingest")
}

// handleIngest — POST /api/v1/ingest (ТЗ § 7.3).
func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	requestID := r.Header.Get("X-Request-Id")
	if requestID == "" {
		requestID = randomID()
	}

	role, ok := s.authRole(r)
	if !ok {
		s.ingest.WithLabelValues("http", "401").Inc()
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error": "token_unknown", "message": "нужен заголовок авторизации со схемой Bearer", "request_id": requestID,
		})
		return
	}
	if role != "ingest" && role != "admin" {
		s.ingest.WithLabelValues("http", "403").Inc()
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "role_denied", "request_id": requestID})
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.opts.MaxBody))
	if err != nil {
		s.ingest.WithLabelValues("http", "413").Inc()
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
			"error": "too_large", "limits": map[string]any{"body_bytes": s.opts.MaxBody}, "request_id": requestID,
		})
		return
	}

	var batch model.Batch
	if err := json.Unmarshal(body, &batch); err != nil {
		s.ingest.WithLabelValues("http", "400").Inc()
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "bad_json", "message": err.Error(), "request_id": requestID,
		})
		return
	}
	if len(batch.Batch) == 0 {
		s.ingest.WithLabelValues("http", "400").Inc()
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "missing_field", "message": "batch пуст", "request_id": requestID,
		})
		return
	}
	if len(batch.Batch) > maxBatchItems {
		s.ingest.WithLabelValues("http", "413").Inc()
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
			"error": "too_large", "limits": map[string]any{"batch_items": maxBatchItems}, "request_id": requestID,
		})
		return
	}

	schemas := make([]int, 0, len(batch.Batch))
	unsupported := 0
	for _, e := range batch.Batch {
		schemas = append(schemas, e.Schema)
		if e.Schema != s.opts.Schema {
			unsupported++
		}
	}
	if unsupported == len(batch.Batch) {
		s.ingest.WithLabelValues("http", "415").Inc()
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]any{
			"error": "unsupported_schema", "supported": []int{s.opts.Schema}, "request_id": requestID,
		})
		return
	}

	accepted, duplicates, rejected := 0, 0, 0
	items := make([]map[string]any, 0, len(batch.Batch))

	for _, e := range batch.Batch {
		out, err := s.store.Ingest(r.Context(), store.EnvelopeInput{
			Envelope:   e,
			Channel:    "http",
			RemoteAddr: clientIP(r),
			RequestID:  requestID,
			Formula:    s.opts.Formula,
		})
		if err != nil {
			s.log.Error("приём конверта", "id", e.ID, "err", err)
			out = store.EnvelopeOutcome{ID: e.ID, Status: store.StatusRejected, Code: 500, Error: "internal"}
		}

		item := map[string]any{"id": out.ID, "status": out.Status}
		switch out.Status {
		case store.StatusOK:
			accepted++
			item["generation"] = out.GenerationID
			if out.Issue != "" {
				item["issue"] = out.Issue
			}
		case store.StatusDuplicate:
			duplicates++
			item["generation"] = out.GenerationID
		default:
			rejected++
			item["error"] = out.Error
			item["message"] = out.Message
		}
		items = append(items, item)
	}

	code := http.StatusOK
	if rejected > 0 {
		code = http.StatusMultiStatus
	}
	s.ingest.WithLabelValues("http", http.StatusText(code)).Inc()
	s.log.Info("приём батча",
		"request_id", requestID, "accepted", accepted, "duplicates", duplicates,
		"rejected", rejected, "ms", time.Since(start).Milliseconds())

	writeJSON(w, code, map[string]any{
		"request_id": requestID,
		"accepted":   accepted,
		"duplicates": duplicates,
		"rejected":   rejected,
		"items":      items,
	})
}

// handleImport — POST /api/v1/import: то же тело файлом (multipart, поле file).
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	role, ok := s.authRole(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "token_unknown"})
		return
	}
	if role != "ingest" && role != "admin" {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "role_denied"})
		return
	}

	if err := r.ParseMultipartForm(s.opts.MaxBody); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad_multipart", "message": err.Error()})
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing_file", "message": "ожидается поле file"})
		return
	}
	defer func() { _ = file.Close() }()

	body, err := io.ReadAll(io.LimitReader(file, s.opts.MaxBody))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad_file"})
		return
	}
	var batch model.Batch
	if err := json.Unmarshal(body, &batch); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad_json", "message": err.Error()})
		return
	}
	if len(batch.Batch) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing_field"})
		return
	}

	accepted, duplicates, rejected := 0, 0, 0
	items := make([]map[string]any, 0, len(batch.Batch))
	for _, e := range batch.Batch {
		out, err := s.store.Ingest(r.Context(), store.EnvelopeInput{
			Envelope: e, Channel: "import", RemoteAddr: clientIP(r), RequestID: randomID(), Formula: s.opts.Formula,
		})
		if err != nil {
			out = store.EnvelopeOutcome{ID: e.ID, Status: store.StatusRejected, Error: "internal"}
		}
		item := map[string]any{"id": out.ID, "status": out.Status}
		switch out.Status {
		case store.StatusOK:
			accepted++
		case store.StatusDuplicate:
			duplicates++
		default:
			rejected++
			item["error"] = out.Error
		}
		items = append(items, item)
	}
	code := http.StatusOK
	if rejected > 0 {
		code = http.StatusMultiStatus
	}
	writeJSON(w, code, map[string]any{"accepted": accepted, "duplicates": duplicates, "rejected": rejected, "items": items})
}

// handleChannels — GET /healthz/channels (ТЗ ФТ-35).
func (s *Server) handleChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := s.store.ChannelsHealth(r.Context(), s.opts.Channels)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "db_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"channels": channels,
		"enabled":  s.opts.Channels,
		"data_channels": dataChannels(s.opts.Channels),
		"closed":        []string{"http80"},
	})
}

// handleReady — GET /readyz/perf: база, миграции, место на диске.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	resp := map[string]any{"ok": true}
	if err := s.store.Ping(ctx); err != nil {
		resp["ok"], resp["db"] = false, "unavailable"
		writeJSON(w, http.StatusServiceUnavailable, resp)
		return
	}
	version, err := s.store.SchemaVersion(ctx)
	if err != nil {
		resp["ok"], resp["db_schema"] = false, 0
		writeJSON(w, http.StatusServiceUnavailable, resp)
		return
	}
	resp["db_schema"] = version
	if free, total, err := diskFree(s.store.Path()); err == nil && total > 0 {
		resp["disk_free_pct"] = store.Percent(free, total)
		resp["disk_free_bytes"] = free
	}
	counts, _ := s.store.Counts(ctx)
	resp["stands"] = counts.Stands
	resp["history"] = counts.History
	writeJSON(w, http.StatusOK, resp)
}

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	return r.RemoteAddr
}

var errNoAuth = errors.New("нет авторизации")

// dataChannels — включённые каналы, которые несут данные (канал 4 не в счёт).
func dataChannels(enabled []string) []string {
	out := make([]string, 0, len(enabled))
	for _, ch := range enabled {
		if ch == "http80" {
			continue
		}
		out = append(out, ch)
	}
	return out
}
