package store

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"
)

// RetentionResult — что сделало ночное обслуживание.
type RetentionResult struct {
	RolledMonths int
	DeletedRows  int64
	LogDeleted   int64
	PurgedFiles  int
	Vacuumed     bool
}

// Reserve выполняет обслуживание по ТЗ § 8.1 и ФТ-54/55:
// свёртка месяца -> проверка -> только затем удаление детальных строк.
// Обратный порядок был бы потерей данных.
func (s *Store) Retention(ctx context.Context, keepMonths int, logDays int) (RetentionResult, error) {
	var res RetentionResult
	if keepMonths <= 0 {
		keepMonths = 12
	}
	if logDays <= 0 {
		logDays = 90
	}

	now := time.Now().UTC()
	cutoff := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -keepMonths, 0)

	// Месяцы для свёртки: все полные месяцы старше окна хранения.
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT strftime('%Y-%m', measured_at, 'unixepoch') AS ym
		   FROM history
		  WHERE measured_at < ?
		  ORDER BY ym`, cutoff.Unix())
	if err != nil {
		return res, fmt.Errorf("поиск месяцев для свёртки: %w", err)
	}
	var months []string
	for rows.Next() {
		var ym string
		if err := rows.Scan(&ym); err != nil {
			_ = rows.Close()
			return res, err
		}
		months = append(months, ym)
	}
	_ = rows.Close()

	for _, ym := range months {
		monthStart, err := time.ParseInLocation("2006-01", ym, time.UTC)
		if err != nil {
			continue
		}
		if err := s.rollupMonth(ctx, monthStart); err != nil {
			return res, err
		}
		res.RolledMonths++
	}

	// Удаляем только те месяцы, у которых свёртка есть и сходится по числу строк.
	for _, ym := range months {
		monthStart, _ := time.ParseInLocation("2006-01", ym, time.UTC)
		monthEnd := monthStart.AddDate(0, 1, 0)

		var srcCount int64
		if err := s.db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM history WHERE measured_at >= ? AND measured_at < ?",
			monthStart.Unix(), monthEnd.Unix()).Scan(&srcCount); err != nil {
			return res, err
		}
		if srcCount == 0 {
			continue
		}

		var rolledSamples int64
		if err := s.db.QueryRowContext(ctx,
			`SELECT COALESCE(SUM(samples), 0) FROM history_monthly WHERE month = ?`,
			monthStart.Unix()).Scan(&rolledSamples); err != nil {
			return res, err
		}
		if rolledSamples < srcCount {
			// Свёртка неполная — удалять нельзя, это была бы потеря данных.
			return res, fmt.Errorf("свёртка месяца %s неполная: строк %d, свёрнуто %d", ym, srcCount, rolledSamples)
		}

		r, err := s.db.ExecContext(ctx,
			"DELETE FROM history WHERE measured_at >= ? AND measured_at < ?",
			monthStart.Unix(), monthEnd.Unix())
		if err != nil {
			return res, fmt.Errorf("удаление %s: %w", ym, err)
		}
		n, _ := r.RowsAffected()
		res.DeletedRows += n
	}

	// Журнал приёма и просроченные команды.
	logCutoff := now.AddDate(0, 0, -logDays).Unix()
	r, err := s.db.ExecContext(ctx, "DELETE FROM ingest_log WHERE received_at < ?", logCutoff)
	if err == nil {
		res.LogDeleted, _ = r.RowsAffected()
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM cmd_queue WHERE status = 'expired' AND created_at < ?", now.AddDate(0, 0, -7).Unix()); err != nil {
		return res, fmt.Errorf("чистка команд: %w", err)
	}

	// Возвращаем место без полного VACUUM (на 1 ядре это минуты).
	if _, err := s.db.ExecContext(ctx, "PRAGMA incremental_vacuum"); err == nil {
		res.Vacuumed = true
	}
	return res, nil
}

// rollupMonth свёртывает один месяц в history_monthly в одной транзакции.
func (s *Store) rollupMonth(ctx context.Context, monthStart time.Time) error {
	monthEnd := monthStart.AddDate(0, 1, 0)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("начало свёртки %s: %w", monthStart.Format("2006-01"), err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx,
		`SELECT generation_id, scenario, COUNT(*), MIN(measured_at), MAX(measured_at),
		        MIN(per_unit_ms), MAX(per_unit_ms), AVG(per_unit_ms), AVG(median_ms), AVG(per_unit_ms)
		   FROM history
		  WHERE measured_at >= ? AND measured_at < ?
		  GROUP BY generation_id, scenario`,
		monthStart.Unix(), monthEnd.Unix())
	if err != nil {
		return fmt.Errorf("чтение месяца %s: %w", monthStart.Format("2006-01"), err)
	}

	type row struct {
		genID    int64
		scenario string
		samples  int64
		from, to int64
		minPer   float64
		maxPer   float64
		medPer   float64
		medMs    float64
	}
	var buf []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.genID, &r.scenario, &r.samples, &r.from, &r.to, &r.minPer, &r.maxPer, &r.medPer, &r.medMs, &r.medPer); err != nil {
			_ = rows.Close()
			return err
		}
		buf = append(buf, r)
	}
	_ = rows.Close()

	now := time.Now().Unix()
	for _, r := range buf {
		per := round3(r.medPer)
		med := round3(r.medMs)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO history_monthly (generation_id, scenario, month, samples, median_ms, per_unit_ms, min_ms, max_ms, covered_from, covered_to, rolled_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(generation_id, scenario, month) DO UPDATE SET
			   samples = excluded.samples, median_ms = excluded.median_ms, per_unit_ms = excluded.per_unit_ms,
			   min_ms = excluded.min_ms, max_ms = excluded.max_ms, covered_from = excluded.covered_from,
			   covered_to = excluded.covered_to, rolled_at = excluded.rolled_at`,
			r.genID, r.scenario, monthStart.Unix(), r.samples, med, per, round3(r.minPer), round3(r.maxPer), r.from, r.to, now); err != nil {
			return fmt.Errorf("свёртка %s/%s: %w", monthStart.Format("2006-01"), r.scenario, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("коммит свёртки %s: %w", monthStart.Format("2006-01"), err)
	}
	return nil
}

// Export делает снимок базы в gzip с манифестом (ТЗ ФТ-56).
// Внешняя копия не автоматизируется (решение О-04): это ручная команда.
func (s *Store) Export(ctx context.Context, path string) (manifestPath string, bytes int64, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", 0, fmt.Errorf("каталог экспорта: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", 0, err
	}
	// VACUUM INTO даёт согласованный снимок без остановки сервиса.
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", tmp); err != nil {
		return "", 0, fmt.Errorf("снимок базы: %w", err)
	}
	defer func() { _ = os.Remove(tmp) }()

	counts, err := s.Counts(ctx)
	if err != nil {
		return "", 0, err
	}
	schemaVersion, _ := s.SchemaVersion(ctx)

	in, err := os.Open(tmp)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(path)
	if err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	gz := gzip.NewWriter(io.MultiWriter(out, hash))
	n, err := io.Copy(gz, in)
	if err != nil {
		_ = gz.Close()
		_ = out.Close()
		return "", 0, fmt.Errorf("сжатие снимка: %w", err)
	}
	if err := gz.Close(); err != nil {
		_ = out.Close()
		return "", 0, err
	}
	if err := out.Close(); err != nil {
		return "", 0, err
	}

	manifest := map[string]any{
		"file":          filepath.Base(path),
		"created_at":    time.Now().UTC().Format(time.RFC3339),
		"db_bytes":      n,
		"sha256":        hex.EncodeToString(hash.Sum(nil)),
		"db_schema":     schemaVersion,
		"stands":        counts.Stands,
		"passports":     counts.Passports,
		"results":       counts.Results,
		"history":       counts.History,
		"ingest_log":    counts.Ingest,
	}
	body, _ := json.MarshalIndent(manifest, "", "  ")

	manifestPath = path + ".manifest.json"
	if err := os.WriteFile(manifestPath, append(body, '\n'), 0o640); err != nil {
		return "", 0, err
	}
	if _, err := s.db.ExecContext(ctx,
		"INSERT INTO audit_log (at, actor, action, target, detail) VALUES (?, 'cli', 'export', ?, ?)",
		time.Now().Unix(), filepath.Base(path), string(body)); err != nil {
		// журнал аудита не должен ломать экспорт
		_ = err
	}
	return manifestPath, n, nil
}

// Check открывает базу и печатает числа — для проверки восстановления.
func (s *Store) Check(ctx context.Context) (string, error) {
	counts, err := s.Counts(ctx)
	if err != nil {
		return "", err
	}
	var monthly int64
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM history_monthly").Scan(&monthly); err != nil {
		return "", err
	}
	version, err := s.SchemaVersion(ctx)
	if err != nil {
		return "", err
	}
	report := fmt.Sprintf("схема=%d стендов=%d паспортов=%d замеров=%d истории=%d свёрток=%d журнал=%d",
		version, counts.Stands, counts.Passports, counts.Results, counts.History, monthly, counts.Ingest)
	return report, nil
}

// Percent — сколько свободного места осталось на диске (для /readyz).
func Percent(free, total uint64) int {
	if total == 0 {
		return 0
	}
	return int(math.Round(float64(free) / float64(total) * 100))
}

var _ = sql.ErrNoRows
