package web

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"perfd/internal/store"
)

// purgeConfirm — обязательное слово подтверждения: без него запрос отвергается,
// чтобы случайный POST (или чужой скрипт в сети) не снёс данные.
const purgeConfirm = "ОЧИСТИТЬ"

type purgeRequest struct {
	Confirm string `json:"confirm"`
	DryRun  bool   `json:"dry_run"`
	DB      *bool  `json:"db"`
	Files   *bool  `json:"files"`
}

// handleAdminPurge — POST /api/admin/purge: очистка данных, полученных обменом.
//
// Права: только роль admin (read/ingest не подходят). Токен подставляет nginx
// на отдельном пути /perf/api/admin/, в браузер он не попадает.
//
// Что удаляется: стенды, поколения, паспорта, замеры, история, агрегаты,
// журнал приёма, аудит, сценарии, очередь команд + файлы каналов обмена.
// Что остаётся: токены, ключи API, версия схемы — иначе очистка отрезала бы
// доступ к витрине и приёму и стала бы необратимой.
func (s *Server) handleAdminPurge(w http.ResponseWriter, r *http.Request) {
	role, ok := s.authRole(r)
	if !ok || role != "admin" {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "forbidden", "message": "нужен токен с ролью admin",
		})
		return
	}

	var req purgeRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "bad_request", "message": "тело: {\"confirm\":\"ОЧИСТИТЬ\",\"dry_run\":true}",
		})
		return
	}
	if req.Confirm != purgeConfirm {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "confirm_required", "message": "слово подтверждения не совпало",
		})
		return
	}

	wantDB, wantFiles := true, true
	if req.DB != nil {
		wantDB = *req.DB
	}
	if req.Files != nil {
		wantFiles = *req.Files
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	resp := map[string]any{"dry_run": req.DryRun, "confirm": true}

	if wantDB {
		rep, err := s.store.PurgeData(ctx, req.DryRun)
		if err != nil {
			s.log.Error("очистка БД не удалась", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error": "db_purge_failed", "message": err.Error(), "db": rep,
			})
			return
		}
		resp["db"] = rep
	}
	if wantFiles {
		resp["files"] = store.PurgeFiles(s.opts.PurgeRoots, req.DryRun)
	}

	// След действия в журнале аудита: сам журнал очищен, без этой строки
	// не осталось бы следов, кто и когда снёс данные.
	if !req.DryRun {
		detail, _ := json.Marshal(map[string]any{"db": resp["db"], "files": resp["files"]})
		if err := s.store.AuditPurge(ctx, role, "all", string(detail)); err != nil {
			s.log.Error("запись аудита очистки не удалась", "err", err)
		}
	}

	// Системные журналы (nginx, dovecot, journald) сервису недоступны:
	// юнит работает как пользователь perf с NoNewPrivileges, а файлы принадлежат root.
	resp["logs"] = map[string]any{
		"cleared": false,
		"note":    "системные журналы чистит только root: tools/portal/deploy/lab/purge-logs-root.sh",
	}

	s.log.Warn("очистка данных портала",
		"role", role, "dry_run", req.DryRun, "db", wantDB, "files", wantFiles, "remote", r.RemoteAddr)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, resp)
}
