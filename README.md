# perfd — сервис портала замеров PerfTest

[![CI](https://github.com/azxasd69/perfd/actions/workflows/ci.yml/badge.svg)](https://github.com/azxasd69/perfd/actions/workflows/ci.yml)
[![Лицензия](https://img.shields.io/badge/лицензия-MIT-green)](LICENSE)

Приёмник и витрина результатов конфигурации [PerfTest](https://github.com/azxasd69/perftest):
принимает конверты от 1С, хранит их в SQLite, показывает стенды, топ, сравнение и историю,
отдаёт API для чтения.

Один статический бинарник без CGO и без зависимостей времени выполнения: на сервере не нужны
ни Go, ни интерпретатор, ни `node_modules`.

## Что делает

- **Приём**: `POST /api/v1/ingest` — конверты `result`, `passport`, `history` (схема 1),
  HMAC-подпись, идемпотентность по `id` конверта, дедупликация повторов.
- **Витрина**: страницы `/`, `/stands`, `/stands/{slug}`, `/top`, `/about` — стенды, замеры,
  динамика, рейтинги.
- **API чтения**: `/api/v1/stands`, `…/results`, `…/history`, `/api/v1/top`, `/api/v1/ratings`,
  `/api/v1/compare`, `/api/v1/meta`, `/api/v1/status`.
- **Служебное**: `/healthz`, `/readyz`, `/healthz/channels`, `/metrics` (Prometheus-формат),
  обход каталога `in/` (`-watch`), мини-DNS для проверки канала (`-dns`), приём почты в
  Maildir (`-mail`), оповещения (`-alerts`).

## Состав

```
cmd/perfd/            запуск, конфигурация, graceful shutdown
internal/config/      чтение env и проверка настроек
internal/model/       контракт конверта (envelope): валидация id, отпечатков, полей
internal/store/       SQLite: открытие, PRAGMA, миграции, ingest, выборки
internal/web/         маршруты, шаблоны, встроенная статика
internal/watch/       обход in/ и подхват файлов
internal/dnssrv/      DNS-канал (53/tcp)
internal/maildrop/    почтовый канал: SMTP → Maildir
internal/alerts/      уведомления о сбоях
internal/status/      состояние каналов и окружения
internal/obs/         JSON-журнал (slog), метрики, middleware
deploy/               unit-файлы systemd, nginx, postfix/dovecot/vsftpd, sshd
docs/                 КПР (архитектура обмена), ТЗ web-контура, ТЗ наблюдаемости, сверки
```

## Сборка и проверки

```powershell
go vet ./...
go test ./...
go build -trimpath -ldflags "-s -w" -o dist/perfd ./cmd/perfd

# кросс-сборка linux/amd64 (как на сервере)
pwsh -File build.ps1
```

## Настройки

Все настройки — переменные окружения с префиксом `PERF_`, значения по умолчанию безопасные:

| Переменная | Смысл |
|---|---|
| `PERF_ADDR` | адрес прослушивания (по умолчанию `127.0.0.1:8080`) |
| `PERF_DB` | файл SQLite (WAL) |
| `PERF_BASE_PATH` | префикс маршрутов (по умолчанию `/perf`) |
| `PERF_INGEST_TOKEN_SHA256` | SHA-256 токена приёма: в конфигурации хранится хеш, не токен |
| `PERF_HMAC_KEY` | ключ HMAC для проверки подписи конвертов |
| `PERF_SCHEMA` | ожидаемая схема конверта (1) |
| `PERF_RETAIN_MONTHS` | сколько хранить замеры |
| `PERF_RETAIN_LOG_DAYS` | сколько хранить журнал приёма |
| `PERF_SMTP_*`, `PERF_MAILDIR` | почтовый канал |
| `PERF_DNS_LISTEN`, `PERF_DNS_ZONE` | DNS-канал |
| `PERF_CHANNELS` | включённые каналы доставки |

## Развёртывание

Юнит-файлы и конфиги — в `deploy/`: `perfd.service`, `app-perfd-watch.service`,
`app-perfd-dns.service`, `app-perfd-mail.service`, `app-perfd-alerts.service`,
`perf-portal.conf` (nginx), `postfix-main.cf` / `postfix-master.cf`, `dovecot-99-perf.conf`,
`vsftpd.conf`, `sshd-99-perf.conf`.

Примеры рассчитаны на отдельный сервисный пользователь и обратный прокси nginx; пути и имена
хостов в конфигах — нейтральные (`perf.example.com`), замените на свои.

## Связь с PerfTest

Клиентская сторона — конфигурация [perftest](https://github.com/azxasd69/perftest)
(модули `ПерфОбмен`, `ПерфОбменТранспорт`, `ПерфОбменШифр`): она собирает конверт, подписывает
его, кладёт в очередь и доставляет по одному из каналов. Контракт приёмника описан в
`internal/model/envelope.go`, требования — в `docs/ТЗ_доработка_интеграция.md` репозитория perftest.

## Документация

| Документ | Что внутри |
|---|---|
| [docs/КПР_Интеграция_сайт.md](docs/КПР_Интеграция_сайт.md) | архитектура обмена: варианты, выбор, API, защита, резервные каналы |
| [docs/ТЗ_web_контур.md](docs/ТЗ_web_контур.md) | требования к серверной части, безопасность, эксплуатация |
| [docs/ТЗ_наблюдаемость_обмена.md](docs/ТЗ_наблюдаемость_обмена.md) | метрики и журналы каналов |
| [docs/Ревью_КПР_Интеграция_сайт.md](docs/Ревью_КПР_Интеграция_сайт.md) | ревью КПР: блокеры и замечания |
| [docs/Сверка_ТЗ_с_КПР.md](docs/Сверка_ТЗ_с_КПР.md), [docs/Сверка_обмена_с_ТЗ.md](docs/Сверка_обмена_с_ТЗ.md) | расхождения документов и реализации |
| [docs/Отчёт_Э-4.md](docs/Отчёт_Э-4.md) | отчёт по этапу Э-4 |

Оговорка: документы ссылаются на приватные скрипты эксплуатации (`tools\vps\*`) — их в
репозитории нет, они остаются у автора. Адреса и имена в примерах заменены на нейтральные.

## Лицензия

MIT — см. [LICENSE](LICENSE). При использовании просьба ссылаться на этот репозиторий.

---

<sub>English: perfd is the receiving portal for PerfTest measurement envelopes — HTTP ingest
with HMAC and idempotency, SQLite storage, read API and dashboard, plus file/DNS/SMTP delivery
channels. Single static binary, MIT licensed. Documentation is in Russian.</sub>