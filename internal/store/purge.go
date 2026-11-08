package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Прицельная очистка данных портала (кнопка «Очистить данные»).
//
// ЧТО УДАЛЯЕТСЯ: всё, что пришло из 1С и получено обменом —
// стенды и их поколения, паспорта, замеры, история, агрегаты, журнал приёма,
// аудит, справочник сценариев, очередь команд.
//
// ЧТО СОХРАНЯЕТСЯ ОСОЗНАННО:
//   - tokens, api_keys — это доступ, а не данные; их удаление отрезало бы витрину от браузера
//     и приём от 1С, то есть очистка стала бы необратимой;
//   - schema_meta — версия схемы (без неё сервис не поднимется);
//   - сама база и её настройки.
//
// Таблицы v1 (results/history/aggregates) и v2 (..._v2 по generation_id) чистятся обе:
// схема 2 пишет в v2, но старые строки в v1 могут остаться от прежних сборок.

// purgeTables — порядок важен: сначала потомки, потом stands.
var purgeTables = []string{
	"history_monthly",
	"results_v2",
	"history_v2",
	"aggregates_v2",
	"stand_generations",
	"stand_passports",
	"results",
	"history",
	"aggregates",
	"cmd_queue",
	"stands",
	"ingest_log",
	"audit_log",
	"scenarios",
}

// PurgeReport — что было, что стало и сколько удалено.
type PurgeReport struct {
	DryRun     bool             `json:"dry_run"`
	Tables     []string         `json:"tables"`
	Before     map[string]int64 `json:"before"`
	After      map[string]int64 `json:"after,omitempty"`
	Deleted    map[string]int64 `json:"deleted,omitempty"`
	RowsTotal  int64            `json:"rows_total"`
	Vacuumed   bool             `json:"vacuumed"`
	DBBytes    int64            `json:"db_bytes,omitempty"`
	DBBytesOld int64            `json:"db_bytes_before,omitempty"`
}

// existingTables — какие из таблиц очистки реально есть в этой базе.
// Состав схемы зависит от применённых миграций (v2-таблицы есть не везде),
// поэтому план строится по факту, а не по списку в коде.
func (s *Store) existingTables(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table'")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]bool, len(purgeTables))
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

func (s *Store) purgePlan(ctx context.Context) ([]string, error) {
	exists, err := s.existingTables(ctx)
	if err != nil {
		return nil, err
	}
	plan := make([]string, 0, len(purgeTables))
	for _, t := range purgeTables {
		if exists[t] {
			plan = append(plan, t)
		}
	}
	return plan, nil
}

func (s *Store) tableCounts(ctx context.Context, plan []string) (map[string]int64, error) {
	out := make(map[string]int64, len(plan))
	for _, t := range plan {
		var n int64
		// имя таблицы — из purgeTables, подстановка безопасна
		err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+t).Scan(&n)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return nil, fmt.Errorf("подсчёт %s: %w", t, err)
		}
		out[t] = n
	}
	return out, nil
}

// PurgeData удаляет данные обмена. При dryRun только считает строки.
func (s *Store) PurgeData(ctx context.Context, dryRun bool) (PurgeReport, error) {
	rep := PurgeReport{DryRun: dryRun}
	plan, err := s.purgePlan(ctx)
	if err != nil {
		return rep, err
	}
	rep.Tables = plan
	before, err := s.tableCounts(ctx, plan)
	if err != nil {
		return rep, err
	}
	rep.Before = before
	for _, n := range before {
		rep.RowsTotal += n
	}
	if dryRun || rep.RowsTotal == 0 {
		return rep, nil
	}
	if p := s.Path(); p != "" {
		if st, err := os.Stat(p); err == nil {
			rep.DBBytesOld = st.Size()
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return rep, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, t := range plan {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+t); err != nil {
			return rep, fmt.Errorf("очистка %s: %w", t, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return rep, err
	}

	after, err := s.tableCounts(ctx, plan)
	if err != nil {
		return rep, err
	}
	rep.After = after
	rep.Deleted = make(map[string]int64, len(before))
	for t, n := range before {
		rep.Deleted[t] = n - after[t]
	}
	rep.RowsTotal = 0
	for _, n := range rep.Deleted {
		rep.RowsTotal += n
	}

	// VACUUM возвращает место на диске; ошибку не считаем фатальной —
	// данные уже удалены, отчёт просто скажет, что уплотнить не удалось.
	if _, err := s.db.ExecContext(ctx, "VACUUM"); err == nil {
		rep.Vacuumed = true
	}
	if p := s.Path(); p != "" {
		if st, err := os.Stat(p); err == nil {
			rep.DBBytes = st.Size()
		}
	}
	return rep, nil
}

// AuditPurge — запись в журнал аудита после очистки: сам журнал тоже чистится,
// поэтому без такой записи действие стало бы непрослеживаемым.
func (s *Store) AuditPurge(ctx context.Context, actor, target, detail string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO audit_log (at, actor, action, target, detail) VALUES (?, ?, 'purge', ?, ?)",
		time.Now().Unix(), actor, target, detail)
	return err
}

// FilePurgeReport — итог удаления файлов обмена.
type FilePurgeReport struct {
	DryRun bool     `json:"dry_run"`
	Files  int      `json:"files"`
	Bytes  int64    `json:"bytes"`
	Dirs   int      `json:"dirs"`
	Errors []string `json:"errors,omitempty"`
	Roots  []string `json:"roots"`
}

// PurgeFiles чистит каталоги обмена: содержимое удаляется, сами каталоги остаются,
// иначе файловый приёмник и nginx WebDAV потеряют точки монтирования.
// Корень может быть и файлом (например снимок базы) — тогда удаляется он сам.
func PurgeFiles(roots []string, dryRun bool) FilePurgeReport {
	rep := FilePurgeReport{DryRun: dryRun}
	for _, root := range roots {
		if root == "" {
			continue
		}
		st, err := os.Stat(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			rep.Errors = append(rep.Errors, root+": "+err.Error())
			continue
		}
		rep.Roots = append(rep.Roots, root)
		if !st.IsDir() {
			rep.Files++
			rep.Bytes += st.Size()
			if !dryRun {
				if err := os.Remove(root); err != nil {
					rep.Errors = append(rep.Errors, root+": "+err.Error())
				}
			}
			continue
		}
		var walk func(dir string) error
		walk = func(dir string) error {
			entries, err := os.ReadDir(dir)
			if err != nil {
				rep.Errors = append(rep.Errors, dir+": "+err.Error())
				return nil
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
			for _, e := range entries {
				full := filepath.Join(dir, e.Name())
				if e.IsDir() {
					if err := walk(full); err != nil {
						return err
					}
					rep.Dirs++
					if !dryRun {
						if err := os.Remove(full); err != nil {
							rep.Errors = append(rep.Errors, full+": "+err.Error())
						}
					}
					continue
				}
				rep.Files++
				if info, err := e.Info(); err == nil {
					rep.Bytes += info.Size()
				}
				if !dryRun {
					if err := os.Remove(full); err != nil {
						rep.Errors = append(rep.Errors, full+": "+err.Error())
					}
				}
			}
			return nil
		}
		if err := walk(root); err != nil && !os.IsNotExist(err) && err != fs.ErrNotExist {
			rep.Errors = append(rep.Errors, root+": "+err.Error())
		}
	}
	return rep
}
