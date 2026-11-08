// Package status: снимок состояния сервера для одностраничника.
// Читает /proc (Linux), диск, числа из БД и доступность портов служб.
// Смысл — одно место, где видно «работает или нет» без захода в консоль.
package status

import (
	"context"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"perfd/internal/store"
)

// Port — проверяемый порт службы.
type Port struct {
	Name string
	Addr string
}

// DefaultPorts — что проверяем по умолчанию (лабораторные адреса, на VDS — тот же набор).
func DefaultPorts(host string) []Port {
	if host == "" {
		host = "127.0.0.1"
	}
	return []Port{
		{"nginx (80)", "127.0.0.1:80"},
		{"nginx (443)", "127.0.0.1:443"},
		{"perfd HTTP", "127.0.0.1:8080"},
		{"FTPS", "127.0.0.1:21"},
		// postfix, dovecot и DNS слушают конкретный адрес, а не loopback.
		{"SMTP submission", host + ":587"},
		{"SMTP TLS", host + ":465"},
		{"IMAP", host + ":993"},
		{"DNS (TCP)", host + ":53"},
	}
}

// PortState — результат проверки одного порта.
type PortState struct {
	Name string `json:"name"`
	Addr string `json:"addr"`
	Open bool   `json:"open"`
}

// Host — факты о машине.
type Host struct {
	UptimeSec      int64   `json:"uptime_sec"`
	Load1          float64 `json:"load1"`
	Load5          float64 `json:"load5"`
	Load15         float64 `json:"load15"`
	CPUCount       int     `json:"cpu_count"`
	MemTotalMB     int64   `json:"mem_total_mb"`
	MemAvailableMB int64   `json:"mem_available_mb"`
	MemUsedPct     int     `json:"mem_used_pct"`
	DiskTotalGB    float64 `json:"disk_total_gb"`
	DiskFreeGB     float64 `json:"disk_free_gb"`
	DiskUsedPct    int     `json:"disk_used_pct"`
}

// Data — объёмы в БД.
type Data struct {
	Stands   int64 `json:"stands"`
	Results  int64 `json:"results"`
	History  int64 `json:"history"`
	Ingest   int64 `json:"ingest_log"`
	DBBytes  int64 `json:"db_bytes"`
	Schema   int   `json:"db_schema"`
	Monthly  int64 `json:"history_monthly"`
}

// Service — состояние самого сервиса.
type Service struct {
	Version    string `json:"version"`
	StartedUTC string `json:"started_utc"`
	UptimeSec  int64  `json:"uptime_sec"`
	DBPath     string `json:"db_path"`
}

// Channel — канал приёма в том виде, как его видит журнал.
type Channel struct {
	ID       string `json:"id"`
	Enabled  bool   `json:"enabled"`
	Accepted int64  `json:"accepted"`
	LastAt   int64  `json:"last_at"`
}

// Snapshot — полный ответ /api/v1/status.
type Snapshot struct {
	Now      string      `json:"now"`
	Overall  string      `json:"overall"`
	Problems []string    `json:"problems"`
	Host     Host        `json:"host"`
	Data     Data        `json:"data"`
	Service  Service     `json:"service"`
	Channels []Channel   `json:"channels"`
	Ports    []PortState `json:"ports"`
}

// Collect собирает снимок. Ошибки чтения не фатальны: поле остаётся нулевым,
// а причина попадает в problems — страница не должна падать из-за отсутствия файла.
func Collect(ctx context.Context, st *store.Store, version string, started time.Time, channels []string, ports []Port) Snapshot {
	snap := Snapshot{
		Now:     time.Now().UTC().Format(time.RFC3339),
		Service: Service{Version: version, StartedUTC: started.UTC().Format(time.RFC3339), UptimeSec: int64(time.Since(started).Seconds()), DBPath: st.Path()},
	}

	snap.Host = collectHost(&snap.Problems)

	if err := st.Ping(ctx); err != nil {
		snap.Problems = append(snap.Problems, "база недоступна: "+err.Error())
	}
	if counts, err := st.Counts(ctx); err == nil {
		snap.Data.Stands = counts.Stands
		snap.Data.Results = counts.Results
		snap.Data.History = counts.History
		snap.Data.Ingest = counts.Ingest
	} else {
		snap.Problems = append(snap.Problems, "счётчики БД: "+err.Error())
	}
	if v, err := st.SchemaVersion(ctx); err == nil {
		snap.Data.Schema = v
	}
	if fi, err := os.Stat(st.Path()); err == nil {
		snap.Data.DBBytes = fi.Size()
	}
	_ = st.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM history_monthly").Scan(&snap.Data.Monthly)

	if health, err := st.ChannelsHealth(ctx, channels); err == nil {
		for _, ch := range channels {
			h := health[ch]
			snap.Channels = append(snap.Channels, Channel{
				ID:       ch,
				Enabled:  true,
				Accepted: toInt64(h["accepted"]),
				LastAt:   toInt64(h["last_at"]),
			})
		}
	}

	for _, p := range ports {
		open := probe(p.Addr)
		snap.Ports = append(snap.Ports, PortState{Name: p.Name, Addr: p.Addr, Open: open})
		if !open {
			snap.Problems = append(snap.Problems, "порт закрыт: "+p.Name+" ("+p.Addr+")")
		}
	}

	switch {
	case len(snap.Problems) == 0:
		snap.Overall = "ok"
	case snap.Data.Stands == 0 && len(snap.Problems) > 3:
		snap.Overall = "down"
	default:
		snap.Overall = "degraded"
	}
	return snap
}

func collectHost(problems *[]string) Host {
	var h Host

	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				h.UptimeSec = int64(v)
			}
		}
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		f := strings.Fields(string(b))
		if len(f) >= 3 {
			h.Load1, _ = strconv.ParseFloat(f[0], 64)
			h.Load5, _ = strconv.ParseFloat(f[1], 64)
			h.Load15, _ = strconv.ParseFloat(f[2], 64)
		}
	}
	if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		h.CPUCount = strings.Count(string(b), "processor\t")
		if h.CPUCount == 0 {
			h.CPUCount = strings.Count(string(b), "processor")
		}
	}
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		var totalKB, availKB int64
		for _, line := range strings.Split(string(b), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			switch fields[0] {
			case "MemTotal:":
				totalKB, _ = strconv.ParseInt(fields[1], 10, 64)
			case "MemAvailable:":
				availKB, _ = strconv.ParseInt(fields[1], 10, 64)
			}
		}
		h.MemTotalMB = totalKB / 1024
		h.MemAvailableMB = availKB / 1024
		if totalKB > 0 {
			h.MemUsedPct = int((totalKB - availKB) * 100 / totalKB)
		}
	}
	if pct, total, free, err := disk("/var/lib/perf"); err == nil {
		h.DiskTotalGB = float64(total) / (1 << 30)
		h.DiskFreeGB = float64(free) / (1 << 30)
		h.DiskUsedPct = pct
	} else {
		*problems = append(*problems, "диск: "+err.Error())
	}
	return h
}

// probe проверяет TCP-порт: локальные службы отвечают мгновенно, 300 мс хватает.
func probe(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func toInt64(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	}
	return 0
}
