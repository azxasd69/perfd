# perfd — сервис портала замеров PerfTest

Каркас web-контура по КПР v3.4 (сторона Web): приём конвертов `schema 1`, хранение в SQLite, страницы и API чтения. Этап **Э-2** плана `tools\vps\План_web_контур.md`: собран и проверен каркас — маршрутизация, БД и миграции, журнал, метрики, страница состояния.

Статический бинарник без CGO и без внешних зависимостей времени выполнения: на VPS не нужны ни Go, ни интерпретатор, ни `node_modules`.

## Состав

```
tools\portal\
  cmd\perfd\main.go            запуск, конфигурация, graceful shutdown
  internal\config\             чтение env-настроек и их проверка
  internal\store\              SQLite: открытие, PRAGMA, миграции, счётчики
  internal\obs\                JSON-журнал (slog), метрики Prometheus-формата, middleware
  internal\web\                маршруты, шаблоны, встроенная статика
    templates\layout.html      каркас страницы (шапка, подвал)
    templates\dashboard.html   страница состояния контура
    assets\app.css             токены кокпита (--pf-*), светлая и тёмная темы
  build.ps1                    vet + тесты + кросс-сборка linux/amd64 (+ windows по флагу)
  dist\                        результат сборки (в git не нужен)
```

## Настройки (переменные окружения)

| Переменная | По умолчанию | Смысл |
|---|---|---|
| `PERF_ADDR` | `127.0.0.1:8080` | адрес прослушивания; на сервере только loopback |
| `PERF_DB` | `perf.db` | путь к файлу SQLite |
| `PERF_BASE_PATH` | `/perf` | префикс публикации; пусто — корень хоста |
| `PERF_MAX_BODY` | `262144` | предел тела запроса, байт |
| `PERF_SCHEMA` | `1` | поддерживаемая версия схемы конверта |
| `PERF_FORMULA_VERSION` | `1` | версия формулы индекса ИИ |
| `PERF_DEFAULT_BASE` | `ven-8327` | стенд сравнения по умолчанию |
| `PERF_INGEST_TOKEN_SHA256` | пусто | SHA-256 hex токена приёма (сам токен не хранится) |
| `PERF_HMAC_KEY` | пусто | ключ HMAC для подписи, base64 |
| `PERF_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |

Секретов в репозитории нет: на сервере они лежат в `/etc/perf/perfd.env` (600, `root:perf`), локально можно не задавать.

## Сборка и запуск

```powershell
pwsh -File tools\portal\build.ps1            # vet + тесты + linux/amd64
pwsh -File tools\portal\build.ps1 -Windows   # плюс локальный exe для проверок
```

Запуск локально (Windows-сборка):

```powershell
$env:PERF_DB = "$env:TEMP\perf-smoke.db"
$env:PERF_BASE_PATH = '/perf'
.\tools\portal\dist\perfd.exe
# http://127.0.0.1:8080/perf/  — страница состояния
```

## Что проверено (08.11.2026)

| Проверка | Результат |
|---|---|
| `go vet ./...` | код 0 |
| `go test ./...` | `store` и `web` — ok (миграции идемпотентны, WAL и FK включены, `503` при закрытой БД, редирект с корня, 404 под префиксом) |
| сборка | `perfd-linux-amd64` 13,0 МБ, `perfd.exe` 13,1 МБ, версия из `git rev-parse --short` |
| дымовой прогон (`scratch\smoke-portal.ps1`) | 9/9: `/perf/healthz` 200 `ok`, `/perf/api/v1/meta` JSON (schema 1, formula 1, db_schema 1), `/perf/metrics` со счётчиками, `/perf/` 2,2 КБ, `/perf/assets/app.css` с `immutable`, `/` → 302 `/perf/`, `/perf/stands` → 404, БД создана и мигрирована |
| память процесса в простое | **14,6 МБ RSS** (в плане закладывалось 30–60) |

## Решения и отступления от КПР

1. **Версия схемы в `schema_meta` — первичный ключ.** В приложении Б КПР колонка объявлена без `PRIMARY KEY`; добавлен первичный ключ по `version`, иначе повторная миграция могла бы записать дубль версии.
2. **Пул соединений ограничен 4** (`SetMaxOpenConns`). SQLite пишет в один поток; ограничение защищает от «слишком много открытых файлов» на 1 ядре.
3. **Префикс публикации — параметр, а не константа.** Он нужен, чтобы портал жил на `/perf` рядом с текущим сайтом (решение Д-2), а позже переехал в корень без правки шаблонов.
4. **Метрики и журнал — без внешних библиотек.** На 1 ядре нужны числа, а не Prometheus-клиент: `/metrics` отдаёт четыре счётчика в текстовом формате.
5. **Встраивание шаблонов и статики через `embed`.** Бинарник самодостаточен: нет отдельного каталога с шаблонами, который можно потерять при деплое.

## Что дальше

- **Э-6 (частично сделано):** страницы и API чтения уже есть (см. раздел «Э-4» ниже); остались графики истории и сравнение стендов на странице.
- **Транспорты каналов 5б/6/7/8:** vsftpd, postfix/dovecot, sshd `Match User perfdrop`, DNS-приёмник — конфиги в ТЗ, на контуре не разворачивались. Файловый приёмник (`perfd -watch`) готов принимать файлы от любого из них.
- **Юниты `app-perfd-watch/-mail/-dns/-alerts`:** сейчас `-watch` запускается вручную, отдельного юнита нет.
- **Оповещения (почта, webhook, `/metrics`-алерты):** не реализованы, кроме счётчиков.

## Э-4: приём конвертов и витрины (08.11.2026)

Реализовано и проверено на тестовом контуре Hyper-V (`172.28.0.10`).

**Схема БД — версия 2** (миграция 002, добавляющая):

- `stand_generations` — поколения стенда по отпечатку среды; замеры и паспорта ссылаются на поколение, а не на стенд (ТЗ Р-10);
- `results`/`history` перестроены на `generation_id`, `aggregates` — на `(generation_id, base_generation_id, formula)`;
- `history_monthly` — месячные свёртки, `scenarios` — справочник кодов, `audit_log`, `tokens`;
- `ingest_log` получил `channel`, `body_sha256`, `issue`, `generation_id`; `stand_passports` — `generation_id`, `body_sha256`, `is_current`.

**Приём** (`POST /perf/api/v1/ingest`, `POST /perf/api/v1/import`):

| Правило | Поведение |
|---|---|
| Токен | `Bearer`, SHA-256 в `tokens` или `PERF_INGEST_TOKEN_SHA256`; нет токена — 401, чужая роль — 403 |
| Схема | `schema != 1` — 415 со списком поддерживаемых |
| Тело | > 256 КиБ — 413; батч > 50 конвертов — 413 |
| Идемпотентность | ключ `id` + хеш значимых полей; повтор — `duplicate` 200, тот же `id` с другим телом — `conflict` 409 |
| Частичный отказ | 207: `ok`/`duplicate`/`rejected` по каждому конверту, транзакция на конверт |
| Паспорт | `Pwd=` в строке соединения маскируется, в журнал идёт `password_in_fields` |
| Замер | `per_unit_ms` сверяется с `median/portion*1000`, расхождение > 0,5 % — замечание `per_unit_mismatch` |
| Поколение | новый `fingerprint` — новое поколение с `parent_generation_id`; история прежнего железа не смешивается |
| ИИ | 100 × среднее(база/стенд) по парным сценариям, округление до 1 знака — как в 1С |

**Витрины и API:** `/perf/`, `/perf/stands`, `/perf/stands/{slug}`, `/perf/stands/{slug}/history/{scenario}`, `/perf/top`, `/perf/about`; `api/v1/{meta,stands,stands/{slug},stands/{slug}/results,stands/{slug}/history,top,ratings,compare}`; `healthz`, `readyz`, `healthz/channels`, `metrics`.

**Режимы:** сервер (по умолчанию), `-watch` (файловый приёмник: `in/` → `out/<id>.ack|.err` → `archive/<дата>/`), `-retention` (свёртка → проверка → удаление, ТЗ § 8.1), `-export` (снимок `VACUUM INTO` + gzip + манифест с SHA-256), `-check` (открывает базу только на чтение).

**Проверено на контуре:** `go vet` — 0; `go test ./...` — ok; смоук `scratch\smoke-perfd.py` — 26/26 и локально, и по `https://172.28.0.10`; файловый канал — `.ack`, архив, `ingest_log.channel='webdav'`; экспорт 7 КБ, восстановление сходится по всем счётчикам; RSS 2,6–3,2 МБ; приём по 80 закрыт (404), по 443 без токена — 401.

Скрипты контура: `deploy\lab\update-lab.sh` (обновление сборки), `checks-lab*.sh`, `checks-watch.sh`, `checks-e4.sh`.

## Транспорты каналов 5а/5б/6/7/8 (08.11.2026)

Развёрнуты на контуре вместе с пятью юнитами. Состав:

| Файл | Что настраивает |
|---|---|
| `deploy\app-perfd.service` | приём HTTP, витрины, API (96 МБ) |
| `deploy\app-perfd-watch.service` | обход `in/webdav|ftps|sftp`, `.ack`/`.err`, архив (64 МБ) |
| `deploy\app-perfd-mail.service` | Maildir `perfing` → конвейер, ack в `perfack` (64 МБ) |
| `deploy\app-perfd-dns.service` | TXT-конверты зоны, 53/tcp, `CAP_NET_BIND_SERVICE` (48 МБ) |
| `deploy\app-perfd-alerts.service` | 4 правила, webhook и почта (32 МБ) |
| `deploy\vsftpd.conf` | FTPS: только TLS, пассивный 30000–30010, chroot `perfdrop` |
| `deploy\postfix-main.cf`, `postfix-master.cf` | submission 587/465 с SASL, доставка только локальная |
| `deploy\dovecot-99-perf.conf` | IMAP 993, passwd-file, SASL-сокет для postfix |
| `deploy\sshd-99-perf.conf` | `Match User perfdrop`: chroot, `internal-sftp`, только ключ |

Скрипты контура: `deploy\lab\install-transports.sh` (пакеты, учётки, каталоги, юниты), `fix-*.sh` (пошаговые исправления, описаны в отчёте § 8.2), `diag-*.sh` (диагностика каналов), `checks-lab*.sh`.

Проверка всех каналов одной командой:

```powershell
python scratch\check-channels.py --token <токен> --ftp-pass <пароль perfdrop> `
    --mail-pass <пароль perfing> --mail-pass-ack <пароль perfack>
```

Ожидаемый результат — 8/8: FTPS, WebDAV, SFTP, SMTP submission, IMAP (оба ящика), DNS и `healthz/channels` со счётчиками по каждому каналу.

**Отступления от Приложения В** (все с причинами, см. § 8.2 отчёта): в `vsftpd.conf` нет `ssl_tlsv1_2/3` (сборка 3.0.5 их не знает), сертификат объявлен до `ssl_enable`, добавлен `check_shell=NO`; `master.cf` взят штатный (без `proxymap` smtpd зависает), chroot у submission выключен; `mail_location` и ack-каталог — `<u>/Maildir`; chroot SFTP — `/srv/perf-sftp` (sshd требует root-owned путь).

## Тестовый контур Hyper-V (08.11.2026)

Стенд, повторяющий среду VDS, живёт в Hyper-V на этой машине: VM `perf-test`, Ubuntu 24.04.5, 1 vCPU, 896 МБ, VHDX 15 ГБ на `D:\hyperv\perf-test`, гость `172.28.0.10` (внутренний коммутатор `perf-lab` + NAT).

Эталонные файлы деплоя — в `tools\portal\deploy\` (`perfd.service`, `perf-portal.conf`), лабораторные шаги — в `deploy\lab\` (`provision-lab.sh`, `tls-lab.sh`, `site-lab.conf`, `install-perfd.sh`, `checks-lab*.sh`). Установка и проверка описаны в `tools\vps\Отчёт_контур_Hyper-V.md`.

Работа с контуром:

```powershell
python scratch\lab_ssh.py --user perfadmin --root --file tools\portal\deploy\lab\checks-lab.sh
pwsh -File tools\vps\deploy.ps1 -Profile hv -LocalDir web\dist
pwsh -File scratch\hv-checkpoint.ps1 -Name run-<что-делаем>   # UAC
```

Важно: приложение живёт под `PERF_BASE_PATH=/perf`, поэтому `/healthz` наружу — это прокси на `127.0.0.1:8080/perf/healthz`; `/nginx-healthz` отдаёт сам nginx.