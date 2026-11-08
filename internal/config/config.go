// Package config читает настройки сервиса из переменных окружения.
// Секретов здесь нет по умолчанию: токен и ключ HMAC приходят снаружи
// (на сервере - из /etc/perf/perfd.env, локально - можно не задавать).
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config - полный набор настроек процесса.
type Config struct {
	Addr        string // адрес прослушивания; на сервере только loopback
	DBPath      string // путь к файлу SQLite
	BasePath    string // префикс публикации, например "/perf"; пусто - корень
	MaxBody     int64  // предел тела запроса в байтах
	Schema      int    // поддерживаемая версия схемы конверта
	Formula     int    // версия формулы индекса ИИ
	DefaultBase string // стенд сравнения по умолчанию
	TokenSHA256 string // sha256 hex токена приёма (сам токен не хранится)
	HMACKey     string // ключ HMAC для подписи, base64
	LogLevel    string // debug|info|warn|error
	Channels    []string // включённые каналы (ТЗ § 6.5)
	RetentionMonths int  // срок хранения детального ряда, мес.
	RetentionLogDays int // срок хранения журнала приёма, дней

	Maildir       string        // /var/mailvhost — Maildir канала 6
	DNSListen     string        // адрес DNS-приёмника, например 91.188.222.193:53
	DNSZone       string        // зона, в которой принимаются TXT-конверты
	AlertEmail    string        // получатель алертов
	AlertFrom     string        // отправитель алертов
	SMTPHost      string        // релей провайдера для писем-алертов
	SMTPPort      int
	SMTPUser      string
	SMTPPass      string
	AlertWebhook  string        // webhook оповещений
	AlertCooldown time.Duration // пауза между повторными алертами одного правила
	StatusHost    string        // адрес машины для проверки портов postfix/dovecot/dns
}

// Load собирает конфигурацию и проверяет её целостность.
func Load() (Config, error) {
	c := Config{
		Addr:        env("PERF_ADDR", "127.0.0.1:8080"),
		DBPath:      env("PERF_DB", "perf.db"),
		BasePath:    env("PERF_BASE_PATH", "/perf"),
		MaxBody:     envInt("PERF_MAX_BODY", 262144),
		Schema:      int(envInt("PERF_SCHEMA", 1)),
		Formula:     int(envInt("PERF_FORMULA_VERSION", 1)),
		DefaultBase: env("PERF_DEFAULT_BASE", "ven-8327"),
		TokenSHA256: strings.TrimSpace(os.Getenv("PERF_INGEST_TOKEN_SHA256")),
		HMACKey:     strings.TrimSpace(os.Getenv("PERF_HMAC_KEY")),
		LogLevel:    env("PERF_LOG_LEVEL", "info"),
		Channels:    splitList(env("PERF_CHANNELS", "http,webdav")),
		RetentionMonths:  int(envInt("PERF_RETAIN_MONTHS", 12)),
		RetentionLogDays: int(envInt("PERF_RETAIN_LOG_DAYS", 90)),
		Maildir:          env("PERF_MAILDIR", "/var/mailvhost"),
		DNSListen:        env("PERF_DNS_LISTEN", "127.0.0.1:53"),
		DNSZone:          env("PERF_DNS_ZONE", "t.perftest.testib.ru"),
		AlertEmail:       env("PERF_ALERT_EMAIL", ""),
		AlertFrom:        env("PERF_ALERT_FROM", "perf@testib.ru"),
		SMTPHost:         env("PERF_SMTP_HOST", ""),
		SMTPPort:         int(envInt("PERF_SMTP_PORT", 587)),
		SMTPUser:         env("PERF_SMTP_USER", ""),
		SMTPPass:         env("PERF_SMTP_PASS", ""),
		AlertWebhook:     env("PERF_ALERT_WEBHOOK", ""),
		AlertCooldown:    envDuration("PERF_ALERT_COOLDOWN", 30*time.Minute),
		StatusHost:       env("PERF_STATUS_HOST", ""),
	}

	if env("PERF_ALLOW_HTTP", "off") != "off" {
		return Config{}, fmt.Errorf("PERF_ALLOW_HTTP должен быть off: канал 4 данные не принимает (ТЗ Р-21)")
	}

	if c.BasePath != "" {
		if !strings.HasPrefix(c.BasePath, "/") {
			return Config{}, fmt.Errorf("PERF_BASE_PATH должен начинаться с '/', получено %q", c.BasePath)
		}
		c.BasePath = strings.TrimRight(c.BasePath, "/")
	}
	if c.DBPath == "" {
		return Config{}, fmt.Errorf("PERF_DB не задан")
	}
	if c.MaxBody <= 0 {
		return Config{}, fmt.Errorf("PERF_MAX_BODY должен быть больше нуля")
	}
	if c.Schema <= 0 || c.Formula <= 0 {
		return Config{}, fmt.Errorf("PERF_SCHEMA и PERF_FORMULA_VERSION должны быть больше нуля")
	}
	return c, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return def
}

func envInt(key string, def int64) int64 {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return n
		}
	}
	return def
}

func splitList(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func envDuration(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok {
		if d, err := time.ParseDuration(strings.TrimSpace(v)); err == nil && d > 0 {
			return d
		}
	}
	return def
}