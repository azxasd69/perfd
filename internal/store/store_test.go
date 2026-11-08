package store

import (
	"context"
	"path/filepath"
	"testing"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestMigrateIdempotent(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("первая миграция: %v", err)
	}
	version, err := st.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if version != 2 {
		t.Fatalf("ожидалась версия схемы 2, получено %d", version)
	}

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("повторная миграция: %v", err)
	}
	version, err = st.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion после повтора: %v", err)
	}
	if version != 2 {
		t.Fatalf("повторная миграция сдвинула версию: %d", version)
	}

	counts, err := st.Counts(ctx)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.Stands != 0 || counts.History != 0 || counts.Results != 0 {
		t.Fatalf("пустая база должна давать нули, получено %+v", counts)
	}
}

func TestPragmas(t *testing.T) {
	st := openTest(t)
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var journalMode string
	if err := st.DB().QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("ожидался WAL, получено %q", journalMode)
	}

	var foreignKeys int
	if err := st.DB().QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("внешние ключи должны быть включены, получено %d", foreignKeys)
	}
}

func TestPing(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	// Ping читает schema_meta, поэтому без миграции база «не готова» — это ожидаемо.
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := st.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}
