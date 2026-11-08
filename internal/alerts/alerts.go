// Package alerts: оповещения по ТЗ § 6.7 — почта, webhook, метрики.
// Правила считаются по данным БД; каждое срабатывание гасится cooldown-ом,
// чтобы письмо не уходило на каждом цикле.
package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"time"

	"perfd/internal/store"
)

// Options — настройки оповещений.
type Options struct {
	Interval  time.Duration
	Webhook   string
	SMTPHost  string
	SMTPPort  int
	SMTPUser  string
	SMTPPass  string
	From      string
	To        string
	Cooldown  time.Duration
	LogPath   string
	Channels  []string
}

// Alert — одно сработавшее правило.
type Alert struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	At       string `json:"at"`
}

// Run проверяет правила по кругу до отмены контекста.
func Run(ctx context.Context, st *store.Store, log *slog.Logger, opts Options) error {
	if opts.Interval <= 0 {
		opts.Interval = 5 * time.Minute
	}
	if opts.Cooldown <= 0 {
		opts.Cooldown = 30 * time.Minute
	}
	last := make(map[string]time.Time)
	ticker := time.NewTicker(opts.Interval)
	defer ticker.Stop()

	log.Info("оповещения запущены", "interval", opts.Interval.String(), "webhook", opts.Webhook != "", "smtp", opts.SMTPHost)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			for _, a := range evaluate(ctx, st, opts) {
				if t, ok := last[a.Rule]; ok && time.Since(t) < opts.Cooldown {
					continue
				}
				last[a.Rule] = time.Now()
				deliver(log, opts, a)
			}
		}
	}
}

// evaluate считает четыре правила (ТЗ § 6.7, сокращённый набор первого этапа):
// отказы приёма, место на диске, молчание канала, размер базы.
func evaluate(ctx context.Context, st *store.Store, opts Options) []Alert {
	var out []Alert
	now := time.Now().UTC().Format(time.RFC3339)

	var rejected int64
	_ = st.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM ingest_log WHERE status = 'rejected' AND received_at > ?",
		time.Now().Add(-15*time.Minute).Unix()).Scan(&rejected)
	if rejected >= 10 {
		out = append(out, Alert{Rule: "ingest_rejected", Severity: "warning",
			Message: fmt.Sprintf("отказов приёма за 15 минут: %d (порог 10)", rejected), At: now})
	}

	var lastOK int64
	_ = st.DB().QueryRowContext(ctx,
		"SELECT COALESCE(MAX(received_at), 0) FROM ingest_log WHERE status = 'ok'").Scan(&lastOK)
	if lastOK > 0 && time.Since(time.Unix(lastOK, 0)) > 24*time.Hour {
		out = append(out, Alert{Rule: "ingest_silent", Severity: "warning",
			Message: fmt.Sprintf("успешных конвертов нет %s", time.Since(time.Unix(lastOK, 0)).Round(time.Hour)), At: now})
	}

	for _, ch := range opts.Channels {
		if ch != "http" && ch != "webdav" && ch != "ftps" && ch != "sftp" && ch != "smtp" && ch != "dns" {
			continue
		}
		var cnt int64
		var lastAt int64
		_ = st.DB().QueryRowContext(ctx,
			"SELECT COUNT(*), COALESCE(MAX(received_at), 0) FROM ingest_log WHERE channel = ?", ch).Scan(&cnt, &lastAt)
		if cnt > 0 && time.Since(time.Unix(lastAt, 0)) > 24*time.Hour {
			out = append(out, Alert{Rule: "channel_stale_" + ch, Severity: "info",
				Message: fmt.Sprintf("канал %s молчит %s", ch, time.Since(time.Unix(lastAt, 0)).Round(time.Hour)), At: now})
		}
	}

	if pct, used, err := diskUsage(opts.LogPath); err == nil {
		if pct >= 90 {
			out = append(out, Alert{Rule: "disk_low", Severity: "critical",
				Message: fmt.Sprintf("занято %d%% файловой системы, база %d байт", pct, used), At: now})
		}
	}
	return out
}

// deliver отправляет алерт в webhook и почту; ошибки только журналируются.
func deliver(log *slog.Logger, opts Options, a Alert) {
	if opts.LogPath != "" {
		if f, err := os.OpenFile(opts.LogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640); err == nil {
			body, _ := json.Marshal(a)
			_, _ = f.Write(append(body, '\n'))
			_ = f.Close()
		}
	}

	if opts.Webhook != "" {
		body, _ := json.Marshal(a)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, opts.Webhook, bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			if resp, err := http.DefaultClient.Do(req); err == nil {
				_ = resp.Body.Close()
			} else {
				log.Warn("webhook не доставлен", "rule", a.Rule, "err", err)
			}
		}
		cancel()
	}

	if opts.SMTPHost == "" || opts.To == "" {
		log.Warn("алерт без получателя", "rule", a.Rule, "message", a.Message)
		return
	}
	addr := net.JoinHostPort(opts.SMTPHost, fmt.Sprint(opts.SMTPPort))
	msg := []byte(fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: [PerfTest] %s: %s\r\nDate: %s\r\n\r\n%s\r\n",
		opts.From, opts.To, a.Severity, a.Rule, time.Now().UTC().Format(time.RFC1123Z), a.Message))
	var auth smtp.Auth
	if opts.SMTPUser != "" {
		auth = smtp.PlainAuth("", opts.SMTPUser, opts.SMTPPass, opts.SMTPHost)
	}
	if err := smtp.SendMail(addr, auth, opts.From, []string{opts.To}, msg); err != nil {
		log.Warn("письмо-алерт не отправлено", "rule", a.Rule, "err", err)
	}
}
