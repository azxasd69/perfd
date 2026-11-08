// Package dnssrv: канал 8 — приёмник TXT-конвертов своей зоны по TCP.
// Формат имени: <chunk>.<seq>.<total>.<id>.<zone>; ответ TXT: part:<seq>/<total>,
// ok:<id> или err:<код>. Вне зоны — REFUSED, рекурсии нет, UDP не слушается (ТЗ ФТ-50).
package dnssrv

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"perfd/internal/model"
	"perfd/internal/store"
)

const (
	typeTXT    = 16
	classIN    = 1
	rcodeOK    = 0
	rcodeFmt   = 1
	rcodeNX    = 3
	rcodeNotImp = 4
	rcodeRefused = 5
	maxPayload = 6144 // ТЗ ФТ-51/53: конверт по каналу 8 не больше 6 КБ
)

// Options — параметры слушателя.
type Options struct {
	Listen  string
	Zone    string
	Formula int
	TTL     time.Duration // сколько держать неполный конверт
}

type part struct {
	total int
	data  map[int][]byte
	at    time.Time
}

// Server — TCP-приёмник DNS.
type Server struct {
	store *store.Store
	log   *slog.Logger
	opts  Options

	mu    sync.Mutex
	parts map[string]*part
}

// New собирает сервер.
func New(st *store.Store, log *slog.Logger, opts Options) *Server {
	if opts.TTL <= 0 {
		opts.TTL = 10 * time.Minute
	}
	opts.Zone = strings.ToLower(strings.TrimSuffix(opts.Zone, "."))
	return &Server{store: st, log: log, opts: opts, parts: make(map[string]*part)}
}

// Run слушает TCP и обрабатывает запросы до отмены контекста.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.opts.Listen)
	if err != nil {
		return fmt.Errorf("bind %s: %w", s.opts.Listen, err)
	}
	s.log.Info("DNS-приёмник слушает", "addr", s.opts.Listen, "zone", s.opts.Zone, "proto", "tcp")

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	go s.janitor(ctx)

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return err
			}
		}
		go s.serve(conn)
	}
}

func (s *Server) janitor(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			s.mu.Lock()
			for id, p := range s.parts {
				if now.Sub(p.at) > s.opts.TTL {
					delete(s.parts, id)
				}
			}
			s.mu.Unlock()
		}
	}
}

func (s *Server) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))

	for {
		var lenBuf [2]byte
		if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
			return
		}
		size := int(binary.BigEndian.Uint16(lenBuf[:]))
		if size < 12 || size > 65535 {
			return
		}
		msg := make([]byte, size)
		if _, err := io.ReadFull(conn, msg); err != nil {
			return
		}

		resp := s.handle(msg)
		if len(resp) == 0 {
			return
		}
		out := make([]byte, 2+len(resp))
		binary.BigEndian.PutUint16(out[:2], uint16(len(resp)))
		copy(out[2:], resp)
		if _, err := conn.Write(out); err != nil {
			return
		}
	}
}

// handle разбирает запрос и возвращает ответ (или nil, если отвечать нечем).
func (s *Server) handle(msg []byte) []byte {
	if len(msg) < 12 {
		return nil
	}
	id := binary.BigEndian.Uint16(msg[0:2])
	flags := binary.BigEndian.Uint16(msg[2:4])
	qdCount := binary.BigEndian.Uint16(msg[4:6])
	opcode := (flags >> 11) & 0x0F

	if opcode != 0 {
		return buildResponse(msg, id, qdCount, rcodeNotImp, classIN, nil)
	}
	if qdCount != 1 {
		return buildResponse(msg, id, qdCount, rcodeFmt, classIN, nil)
	}

	name, qtype, qclass, qEnd, ok := parseQuestion(msg, 12)
	if !ok {
		return buildResponse(msg, id, qdCount, rcodeFmt, classIN, nil)
	}
	// Сравнение зоны — без регистра, но сами метки сохраняются как есть:
	// имя содержит base64url, где регистр значим.
	trimmed := strings.TrimSuffix(name, ".")

	if !s.inZone(trimmed) {
		return buildResponse(msg[:qEnd], id, 1, rcodeRefused, qclass, nil)
	}
	if qtype != typeTXT {
		// Прочие типы в зоне: пустой авторитативный ответ NOERROR.
		return buildResponse(msg[:qEnd], id, 1, rcodeOK, qclass, nil)
	}

	labels := s.stripZone(trimmed)
	if len(labels) == 0 {
		return buildResponse(msg[:qEnd], id, 1, rcodeNX, qclass, nil)
	}
	if labels[0] == "probe" {
		return buildResponse(msg[:qEnd], id, 1, rcodeOK, qclass, [][]byte{[]byte("ok")})
	}

	text, rcode := s.collect(labels)
	return buildResponse(msg[:qEnd], id, 1, rcode, qclass, [][]byte{[]byte(text)})
}

func (s *Server) inZone(name string) bool {
	lower := strings.ToLower(name)
	if lower == s.opts.Zone || strings.HasSuffix(lower, "."+s.opts.Zone) {
		return true
	}
	return false
}

func (s *Server) stripZone(name string) []string {
	if strings.EqualFold(name, s.opts.Zone) {
		return nil
	}
	head := name[:len(name)-len(s.opts.Zone)-1]
	return strings.Split(head, ".")
}

// collect собирает части конверта и отдаёт текст ответа.
// Имя: <chunk>.<seq>.<total>.<id>.<zone>; допускается и без total: <chunk>.<seq>.<id>.
func (s *Server) collect(labels []string) (string, int) {
	n := len(labels)
	var (
		seq, total  int
		id          string
		chunkLabels []string
	)
	if n >= 4 {
		if t, err := strconv.Atoi(labels[n-2]); err == nil && t > 0 {
			if q, err := strconv.Atoi(labels[n-3]); err == nil {
				seq, total, id = q, t, labels[n-1]
				chunkLabels = labels[:n-3]
			}
		}
	}
	if id == "" && n >= 3 {
		if q, err := strconv.Atoi(labels[n-2]); err == nil {
			seq, total, id = q, 1, labels[n-1]
			chunkLabels = labels[:n-2]
		}
	}
	if id == "" || len(chunkLabels) == 0 || seq < 1 || total < 1 || total > 64 || seq > total {
		return "err:400", rcodeOK
	}

	raw, err := base64.RawURLEncoding.DecodeString(strings.Join(chunkLabels, ""))
	if err != nil {
		return "err:400", rcodeOK
	}

	s.mu.Lock()
	p, ok := s.parts[id]
	if !ok || p.total != total || time.Since(p.at) > s.opts.TTL {
		p = &part{total: total, data: make(map[int][]byte), at: time.Now()}
		s.parts[id] = p
	}
	p.data[seq] = raw
	p.at = time.Now()

	if len(p.data) < p.total {
		s.mu.Unlock()
		return fmt.Sprintf("part:%d/%d", len(p.data), p.total), rcodeOK
	}

	buf := make([]byte, 0, 4096)
	for i := 1; i <= p.total; i++ {
		buf = append(buf, p.data[i]...)
	}
	delete(s.parts, id)
	s.mu.Unlock()

	if len(buf) > maxPayload {
		return "err:413", rcodeOK
	}
	return s.ingest(id, buf), rcodeOK
}

// ingest прогоняет собранный конверт и отдаёт текст подтверждения.
func (s *Server) ingest(id string, payload []byte) string {
	var batch model.Batch
	if err := json.Unmarshal(payload, &batch); err != nil {
		return "err:400"
	}
	if len(batch.Batch) == 0 {
		return "err:400"
	}
	code := 200
	for _, env := range batch.Batch {
		out, err := s.store.Ingest(context.Background(), store.EnvelopeInput{
			Envelope: env, Channel: "dns", RemoteAddr: "dns", RequestID: id, Formula: s.opts.Formula,
		})
		if err != nil {
			return "err:500"
		}
		switch out.Status {
		case store.StatusOK, store.StatusDuplicate:
		default:
			if out.Code >= 400 {
				code = out.Code
			}
		}
	}
	if code != 200 {
		return fmt.Sprintf("err:%d", code)
	}
	return "ok:" + id
}

// parseQuestion разбирает первый вопрос, возвращает имя, тип, класс и смещение конца.
func parseQuestion(msg []byte, off int) (name string, qtype, qclass uint16, end int, ok bool) {
	var labels []string
	for {
		if off >= len(msg) {
			return "", 0, 0, 0, false
		}
		l := int(msg[off])
		off++
		if l == 0 {
			break
		}
		if l&0xC0 != 0 || off+l > len(msg) {
			return "", 0, 0, 0, false
		}
		labels = append(labels, string(msg[off:off+l]))
		off += l
	}
	if off+4 > len(msg) {
		return "", 0, 0, 0, false
	}
	qtype = binary.BigEndian.Uint16(msg[off : off+2])
	qclass = binary.BigEndian.Uint16(msg[off+2 : off+4])
	return strings.Join(labels, "."), qtype, qclass, off + 4, true
}

// buildResponse собирает ответ: шапка, вопрос (если есть), один TXT-ответ.
func buildResponse(question []byte, id uint16, qd uint16, rcode int, class uint16, txt [][]byte) []byte {
	resp := make([]byte, 12, 128)
	binary.BigEndian.PutUint16(resp[0:2], id)
	// QR=1, AA=1, TC=0, RD=0, RA=0, Z=0, RCODE
	flags := uint16(0x8000 | 0x0400)
	flags |= uint16(rcode & 0x0F)
	binary.BigEndian.PutUint16(resp[2:4], flags)
	binary.BigEndian.PutUint16(resp[4:6], qd)
	if len(txt) > 0 {
		binary.BigEndian.PutUint16(resp[6:8], 1)
	}
	resp = append(resp, question[12:]...)

	if len(txt) == 0 {
		return resp
	}

	// Answer: имя — указатель на вопрос, TXT, IN, TTL 60.
	ans := []byte{0xC0, 0x0C, 0x00, typeTXT, 0x00, classIN, 0x00, 0x00, 0x00, 0x3C}
	var rdata []byte
	for _, chunk := range txt {
		for len(chunk) > 255 {
			rdata = append(rdata, 255)
			rdata = append(rdata, chunk[:255]...)
			chunk = chunk[255:]
		}
		rdata = append(rdata, byte(len(chunk)))
		rdata = append(rdata, chunk...)
	}
	rdlen := make([]byte, 2)
	binary.BigEndian.PutUint16(rdlen, uint16(len(rdata)))
	ans = append(ans, rdlen...)
	ans = append(ans, rdata...)
	return append(resp, ans...)
}

// SortParts — для тестов: стабильный порядок сборки.
func SortParts(m map[int][]byte) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}
