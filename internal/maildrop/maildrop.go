// Package maildrop: канал 6 — приём конвертов из Maildir perfing и подтверждения в perfack.
// Вложение application/json извлекается, тело письма игнорируется (ТЗ ФТ-47/48).
package maildrop

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"time"

	"perfd/internal/model"
	"perfd/internal/store"
)

// Options — каталоги и параметры обхода почты.
type Options struct {
	MailRoot string // /var/mailvhost
	User     string // perfing — входящие конверты
	AckUser  string // perfack — исходящие подтверждения
	MaxBytes int64  // предел вложения, 5 МБ
	Formula  int
	Interval time.Duration
}

// Ack — тело письма-подтверждения (то же, что out/<id>.ack).
type Ack struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Code   int    `json:"code"`
	At     string `json:"at"`
	Error  string `json:"error,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Run обходит Maildir perfing/new до отмены контекста.
func Run(ctx context.Context, st *store.Store, log *slog.Logger, opts Options) error {
	if opts.MailRoot == "" {
		opts.MailRoot = "/var/mailvhost"
	}
	if opts.User == "" {
		opts.User = "perfing"
	}
	if opts.AckUser == "" {
		opts.AckUser = "perfack"
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = 5 << 20
	}
	if opts.Interval <= 0 {
		opts.Interval = 10 * time.Second
	}

	inBase := mailboxPath(opts.MailRoot, opts.User)
	ackBase := mailboxPath(opts.MailRoot, opts.AckUser)
	for _, d := range []string{inBase, ackBase} {
		for _, sub := range []string{"new", "cur", "tmp"} {
			if err := os.MkdirAll(filepath.Join(d, sub), 0o770); err != nil {
				return fmt.Errorf("каталог %s/%s: %w", d, sub, err)
			}
		}
	}

	log.Info("почтовый приёмник запущен", "in", filepath.Join(inBase, "new"), "ack", ackBase)
	ticker := time.NewTicker(opts.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			scan(ctx, st, log, opts, inBase, ackBase)
		}
	}
}

// mailboxPath возвращает каталог Maildir учётки.
// postfix с home_mailbox=Maildir/ кладёт письма в <home>/Maildir,
// поэтому основной путь — он; старый вариант (<home>) поддерживается для совместимости.
func mailboxPath(root, user string) string {
	withMaildir := filepath.Join(root, user, "Maildir")
	if info, err := os.Stat(withMaildir); err == nil && info.IsDir() {
		return withMaildir
	}
	return filepath.Join(root, user)
}

func scan(ctx context.Context, st *store.Store, log *slog.Logger, opts Options, inDir, ackDir string) {
	entries, err := os.ReadDir(filepath.Join(inDir, "new"))
	if err != nil {
		log.Error("чтение Maildir", "err", err)
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(inDir, "new", e.Name())
		ack, err := process(ctx, st, opts, path)
		if err != nil {
			log.Error("письмо не обработано", "file", e.Name(), "err", err)
			ack = Ack{ID: e.Name(), Status: "rejected", Code: 400, Error: "unreadable"}
		}
		if ack.ID == "" {
			ack.ID = e.Name()
		}
		ack.At = time.Now().UTC().Format(time.RFC3339)
		if err := deliver(ackDir, ack); err != nil {
			log.Error("подтверждение не доставлено", "ack", ack.ID, "err", err)
		}
		moveToCur(inDir, e.Name())
		log.Info("письмо обработано", "file", e.Name(), "status", ack.Status, "id", ack.ID, "error", ack.Error)
	}
}

func process(ctx context.Context, st *store.Store, opts Options, path string) (Ack, error) {
	raw, err := os.Open(path)
	if err != nil {
		return Ack{}, err
	}
	defer func() { _ = raw.Close() }()

	msg, err := mail.ReadMessage(raw)
	if err != nil {
		return Ack{Status: "rejected", Code: 400, Error: "bad_mime"}, nil
	}

	payload, err := findJSON(msg)
	if err != nil {
		return Ack{Status: "rejected", Code: 400, Error: err.Error()}, nil
	}
	if int64(len(payload)) > opts.MaxBytes {
		return Ack{Status: "rejected", Code: 413, Error: "too_large"}, nil
	}

	var batch model.Batch
	if err := json.Unmarshal(payload, &batch); err != nil {
		return Ack{Status: "rejected", Code: 400, Error: "bad_json"}, nil
	}
	if len(batch.Batch) == 0 {
		return Ack{Status: "rejected", Code: 400, Error: "empty_batch"}, nil
	}

	ack := Ack{Status: "ok", Code: 200}
	accepted, duplicates, rejected := 0, 0, 0
	for _, env := range batch.Batch {
		out, err := st.Ingest(ctx, store.EnvelopeInput{
			Envelope: env, Channel: "smtp", RemoteAddr: "maildir", RequestID: filepath.Base(path), Formula: opts.Formula,
		})
		if err != nil {
			rejected++
			ack.Error = "internal"
			continue
		}
		if ack.ID == "" {
			ack.ID = out.ID
		}
		switch out.Status {
		case store.StatusOK:
			accepted++
		case store.StatusDuplicate:
			duplicates++
		default:
			rejected++
			ack.Error = out.Error
			ack.Detail = out.Message
		}
	}
	switch {
	case rejected > 0 && accepted == 0 && duplicates == 0:
		ack.Status, ack.Code = "rejected", 400
	case rejected > 0:
		ack.Status, ack.Code = "partial", 207
	case duplicates > 0 && accepted == 0:
		ack.Status = "duplicate"
	}
	return ack, nil
}

// findJSON ищет часть с Content-Type application/json (или имя .json)
// и снимает transfer-encoding: почтовые клиенты и 1С шлют вложения в base64.
func findJSON(msg *mail.Message) ([]byte, error) {
	mediaType, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if strings.HasPrefix(mediaType, "multipart/") {
		reader := multipart.NewReader(msg.Body, params["boundary"])
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("bad_mime")
			}
			partType, partParams, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
			if partType == "application/json" ||
				strings.HasSuffix(partParams["name"], ".json") ||
				strings.HasSuffix(partParams["filename"], ".json") {
				body, err := io.ReadAll(part)
				if err != nil {
					return nil, fmt.Errorf("bad_mime")
				}
				return decodePart(body, part.Header.Get("Content-Transfer-Encoding"))
			}
		}
		return nil, fmt.Errorf("no_json_attachment")
	}
	if mediaType == "application/json" {
		body, err := io.ReadAll(msg.Body)
		if err != nil {
			return nil, fmt.Errorf("bad_mime")
		}
		return decodePart(body, msg.Header.Get("Content-Transfer-Encoding"))
	}
	return nil, fmt.Errorf("no_json_attachment")
}

// decodePart снимает Content-Transfer-Encoding и отдаёт байты вложения.
func decodePart(body []byte, enc string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(enc)) {
	case "base64":
		clean := strings.Map(func(r rune) rune {
			if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, string(body))
		decoded, err := base64.StdEncoding.DecodeString(clean)
		if err != nil {
			return nil, fmt.Errorf("bad_base64")
		}
		return decoded, nil
	case "quoted-printable":
		decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(body)))
		if err != nil {
			return nil, fmt.Errorf("bad_qp")
		}
		return decoded, nil
	default:
		return body, nil
	}
}

// deliver кладёт письмо-подтверждение в Maildir perfack (tmp -> new, атомарно).
func deliver(ackDir string, ack Ack) error {
	body, _ := json.Marshal(ack)
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: perfd <perfd@testib.ru>\r\n")
	fmt.Fprintf(&buf, "To: perfack@testib.ru\r\n")
	fmt.Fprintf(&buf, "Subject: perf ack %s\r\n", ack.ID)
	fmt.Fprintf(&buf, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&buf, "Message-ID: <%d.%s@perf>\r\n", time.Now().UnixNano(), ack.ID)
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: application/json; charset=utf-8\r\n")
	fmt.Fprintf(&buf, "Content-Disposition: attachment; filename=\"%s.ack.json\"\r\n\r\n", ack.ID)
	buf.Write(body)
	buf.WriteString("\r\n")

	name := fmt.Sprintf("%d.%d.perf", time.Now().UnixNano(), os.Getpid())
	tmp := filepath.Join(ackDir, "tmp", name)
	if err := os.WriteFile(tmp, buf.Bytes(), 0o660); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(ackDir, "new", name))
}

// moveToCur переносит обработанное письмо из new/ в cur/ (стандарт Maildir).
func moveToCur(maildir, name string) {
	_ = os.Rename(
		filepath.Join(maildir, "new", name),
		filepath.Join(maildir, "cur", name+":2,S"),
	)
}
