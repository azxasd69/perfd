#!/usr/bin/env bash
# Обновление perfd в тестовом контуре до текущей сборки (Э-4).
# Запуск от root: bash /tmp/update-lab.sh
# Ожидает на госте: /tmp/perfd (бинарник), /tmp/perf-portal.conf, /tmp/site-lab.conf
set -euo pipefail

echo "== 0. исходное состояние =="
systemctl is-active perfd || true
/usr/local/bin/perfd -version 2>/dev/null || echo "(версия прежнего бинарника неизвестна)"

echo "== 1. каталоги файлового приёмника =="
install -d -o perf -g perf -m 775 /var/lib/perf/in /var/lib/perf/out /var/lib/perf/archive

echo "== 2. бинарник =="
install -m 755 /tmp/perfd /usr/local/bin/perfd
/usr/local/bin/perfd -version

echo "== 3. окружение (токен сохраняется) =="
TOKEN_SHA=$(grep '^PERF_INGEST_TOKEN_SHA256=' /etc/perf/perfd.env | cut -d= -f2)
cat > /etc/perf/perfd.env <<ENV
PERF_ADDR=127.0.0.1:8080
PERF_DB=/var/lib/perf/perf.db
PERF_BASE_PATH=/perf
PERF_MAX_BODY=262144
PERF_SCHEMA=1
PERF_FORMULA_VERSION=1
PERF_DEFAULT_BASE=база-вендор
PERF_INGEST_TOKEN_SHA256=${TOKEN_SHA}
PERF_CHANNELS=http,webdav
PERF_RETAIN_MONTHS=12
PERF_RETAIN_LOG_DAYS=90
PERF_ALLOW_HTTP=off
PERF_LOG_LEVEL=info
ENV
chown root:perf /etc/perf/perfd.env
chmod 640 /etc/perf/perfd.env

echo "== 4. nginx: 80 без приёма, 443 с приёмом =="
install -m 644 /tmp/perf-portal.conf /etc/nginx/snippets/perf-portal.conf
install -m 644 /tmp/site-lab.conf /etc/nginx/sites-available/testib.ru
nginx -t
systemctl reload nginx

echo "== 5. перезапуск и миграция =="
systemctl restart perfd
sleep 2
systemctl is-active perfd
journalctl -u perfd -n 8 --no-pager | tail -8

echo "== 6. проверки =="
echo -n "healthz: "; curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/perf/healthz
echo -n "meta: "; curl -s http://127.0.0.1:8080/perf/api/v1/meta | head -c 400; echo
echo -n "схема БД: "; sqlite3 /var/lib/perf/perf.db 'select max(version) from schema_meta;'
echo -n "8020 ingest (80, должен быть 404): "; curl -s -o /dev/null -w '%{http_code}\n' -X POST -H 'Host: testib.ru' http://127.0.0.1/perf/api/v1/ingest
echo -n "8020 ingest (443, без токена — 401): "; curl -sk -o /dev/null -w '%{http_code}\n' -X POST https://127.0.0.1/perf/api/v1/ingest
echo -n "RSS: "; systemctl show perfd -p MemoryCurrent --value | awk '{printf "%.1f МБ\n", $1/1048576}'
echo -n "токен: "; cat /root/lab-perf-token
