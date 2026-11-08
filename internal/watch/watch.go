// Package watch: файловый приёмник (каналы 5а WebDAV, 5б FTPS, 7 SFTP).
// Все три транспорта кладут файл в один каталог in/ и дальше работают одинаково:
// обход -> Ingest -> out/<id>.ack | out/<id>.err -> archive/<дата>/.
package watch

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"perfd/internal/model"
	"perfd/internal/store"
)

// Options — каталоги и параметры обходчика.
type Options struct {
	InDir     string
	OutDir    string
	ArchiveDir string
	Channel   string // имя канала для ingest_log: webdav | ftps | sftp
	Formula   int
	StableFor time.Duration
	Interval  time.Duration
}

// Ack — содержимое файла подтверждения.
type Ack struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Code   int    `json:"code"`
	At     string `json:"at"`
	Error  string `json:"error,omitempty"`
}

// Run обходит каталог до отмены контекста. Возврат — по ctx.Done().
func Run(ctx context.Context, st *store.Store, log *slog.Logger, opts Options) error {
	if opts.StableFor <= 0 {
		opts.StableFor = 2 * time.Second
	}
	if opts.Interval <= 0 {
		opts.Interval = 15 * time.Second
	}
	if opts.Channel == "" {
		opts.Channel = "webdav"
	}
	for _, dir := range []string{opts.InDir, opts.OutDir, opts.ArchiveDir} {
		if err := os.MkdirAll(dir, 0o775); err != nil {
			return fmt.Errorf("каталог %s: %w", dir, err)
		}
	}

	lastSize := make(map[string]int64)
	ticker := time.NewTicker(opts.Interval)
	defer ticker.Stop()

	log.Info("обход файлового приёмника запущен", "in", opts.InDir, "channel", opts.Channel)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			scan(ctx, st, log, opts, lastSize)
		}
	}
}

// scan обрабатывает готовые файлы: размер не менялся StableFor.
func scan(ctx context.Context, st *store.Store, log *slog.Logger, opts Options, lastSize map[string]int64) {
	entries, err := os.ReadDir(opts.InDir)
	if err != nil {
		log.Error("чтение каталога приёма", "err", err)
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		path := filepath.Join(opts.InDir, name)

		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		// Готовность: размер стабилен и файл не свежее StableFor.
		if prev, ok := lastSize[name]; !ok || prev != info.Size() || time.Since(info.ModTime()) < opts.StableFor {
			lastSize[name] = info.Size()
			continue
		}
		delete(lastSize, name)

		body, err := os.ReadFile(path)
		if err != nil {
			log.Error("чтение файла", "file", name, "err", err)
			continue
		}

		var batch model.Batch
		if err := json.Unmarshal(body, &batch); err != nil {
			writeResult(opts, strings.TrimSuffix(name, ".json"), Ack{Status: "rejected", Code: 400, Error: "bad_json"})
			archive(opts, path)
			continue
		}

		id := strings.TrimSuffix(name, ".json")
		status, code := "ok", 200
		accepted, duplicates, rejected := 0, 0, 0
		for _, env := range batch.Batch {
			out, err := st.Ingest(ctx, store.EnvelopeInput{
				Envelope: env, Channel: opts.Channel, RemoteAddr: "file:" + name, RequestID: id, Formula: opts.Formula,
			})
			if err != nil {
				log.Error("приём из файла", "file", name, "err", err)
				rejected++
				continue
			}
			switch out.Status {
			case store.StatusOK:
				accepted++
			case store.StatusDuplicate:
				duplicates++
			default:
				rejected++
			}
		}
		if rejected > 0 && accepted == 0 && duplicates == 0 {
			status, code = "rejected", 400
		} else if rejected > 0 {
			status, code = "partial", 207
		} else if duplicates > 0 && accepted == 0 {
			status = "duplicate"
		}
		writeResult(opts, id, Ack{ID: id, Status: status, Code: code})
		log.Info("файл обработан", "file", name, "accepted", accepted, "duplicates", duplicates, "rejected", rejected)
		archive(opts, path)
	}
}

func writeResult(opts Options, id string, ack Ack) {
	ack.At = time.Now().UTC().Format(time.RFC3339)
	body, _ := json.Marshal(ack)
	ext := ".ack"
	if ack.Status == "rejected" {
		ext = ".err"
	}
	target := filepath.Join(opts.OutDir, id+ext)
	if err := os.WriteFile(target, append(body, '\n'), 0o644); err != nil {
		// Подтверждение — не критичный путь: файл уйдёт в архив, 1С повторит.
		_ = err
	}
}

func archive(opts Options, path string) {
	day := time.Now().UTC().Format("2006-01-02")
	dir := filepath.Join(opts.ArchiveDir, day)
	if err := os.MkdirAll(dir, 0o775); err != nil {
		return
	}
	_ = os.Rename(path, filepath.Join(dir, filepath.Base(path)))
}
