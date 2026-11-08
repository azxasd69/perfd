package web

import (
	"net/http"
	"time"

	"perfd/internal/status"
)

// handleStatus — GET /api/v1/status: снимок состояния сервера для одностраничника.
// Отдаётся без авторизации: только агрегаты, без паспортов и токенов.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxWithTimeout(4 * time.Second)
	defer cancel()

	snap := status.Collect(ctx, s.store, Version, s.started, s.opts.Channels, status.DefaultPorts(s.opts.StatusHost))
	code := http.StatusOK
	if snap.Overall == "down" {
		code = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	enc := newJSONEncoder(w)
	_ = enc.Encode(snap)
}
