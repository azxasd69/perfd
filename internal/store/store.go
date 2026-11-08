// Package store: доступ к SQLite. Одна зависимость (modernc.org/sqlite, чистый Go),
// WAL и busy_timeout выставлены на уровне DSN, чтобы не забыть их в коде.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Store - обёртка над пулом соединений SQLite.
type Store struct {
	db   *sql.DB
	path string
}

// Counts - сводка объёмов для страницы состояния.
type Counts struct {
	Stands    int64
	Passports int64
	Results   int64
	History   int64
	Ingest    int64
}

// Open открывает базу и проверяет соединение.
func Open(path string) (*Store, error) {
	dsn := "file:" + path +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("открытие базы %s: %w", path, err)
	}
	// SQLite пишет в один поток; чтения идут параллельно, но пул держим небольшим.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(time.Hour)

	s := &Store{db: db, path: path}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("проверка соединения с %s: %w", path, err)
	}
	return s, nil
}

// Close закрывает пул.
func (s *Store) Close() error { return s.db.Close() }

// Path возвращает путь к файлу базы (для страницы состояния).
func (s *Store) Path() string { return s.path }

// DB отдаёт пул для обработчиков, которым нужны выборки (приём появится в Э-4).
func (s *Store) DB() *sql.DB { return s.db }

// Ping проверяет живость базы - на этом построен /healthz.
// Важно читать РЕАЛЬНУЮ таблицу: "SELECT 1" не трогает файл и не замечает
// ни блокировку базы, ни повреждение - проверено в контуре 08.11.2026.
func (s *Store) Ping(ctx context.Context) error {
	var n int
	return s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_meta").Scan(&n)
}

// OpenReadOnly открывает базу только на чтение: нужно для проверки
// восстановленного снимка, где каталог может быть недоступен на запись.
// Автомиграции здесь запрещены: проверка не должна менять файл.
func OpenReadOnly(path string) (*Store, error) {
	dsn := "file:" + path + "?mode=ro&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("открытие базы %s только на чтение: %w", path, err)
	}
	db.SetMaxOpenConns(2)
	s := &Store{db: db, path: path}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("проверка соединения с %s: %w", path, err)
	}
	return s, nil
}

// Migrate применяет все неприменённые миграции в одной транзакции каждая.
// Повторный вызов безопасен: версия проверяется в schema_meta.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_meta (version INTEGER NOT NULL PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("создание schema_meta: %w", err)
	}

	for i, script := range migrations {
		version := i + 1

		var applied int
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_meta WHERE version = ?", version).Scan(&applied); err != nil {
			return fmt.Errorf("чтение версии %d: %w", version, err)
		}
		if applied > 0 {
			continue
		}

		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("начало транзакции миграции %d: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, script); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("миграция %d: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO schema_meta (version, applied_at) VALUES (?, ?)", version, time.Now().Unix()); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("фиксация версии %d: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("коммит миграции %d: %w", version, err)
		}
	}
	return nil
}

// SchemaVersion возвращает текущую применённую версию схемы (0 - пустая база).
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v sql.NullInt64
	err := s.db.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_meta").Scan(&v)
	if err != nil {
		return 0, err
	}
	if !v.Valid {
		return 0, nil
	}
	return int(v.Int64), nil
}

// Counts считает строки по таблицам - для дашборда и проверок.
func (s *Store) Counts(ctx context.Context) (Counts, error) {
	var c Counts
	queries := []struct {
		dst *int64
		sql string
	}{
		{&c.Stands, "SELECT COUNT(*) FROM stands"},
		{&c.Passports, "SELECT COUNT(*) FROM stand_passports"},
		{&c.Results, "SELECT COUNT(*) FROM results"},
		{&c.History, "SELECT COUNT(*) FROM history"},
		{&c.Ingest, "SELECT COUNT(*) FROM ingest_log"},
	}
	for _, q := range queries {
		if err := s.db.QueryRowContext(ctx, q.sql).Scan(q.dst); err != nil {
			return c, err
		}
	}
	return c, nil
}

// ErrNotFound возвращают обработчики, когда объект не найден.
var ErrNotFound = errors.New("не найдено")
