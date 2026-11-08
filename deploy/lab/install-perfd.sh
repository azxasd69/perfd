#!/usr/bin/env bash
# Установка и запуск perfd в контуре (П-3/П-4). Запускается от root.
# Ожидает: бинарник уже положен в /tmp/perfd, эталонные файлы — в /tmp/perf-deploy/.
set -euo pipefail

echo "== 1. пользователь, каталоги, бинарник =="
id perf >/dev/null 2>&1 || adduser --system --group --home /var/lib/perf --no-create-home perf >/dev/null
install -d -o perf -g perf -m 750 /var/lib/perf /var/log/perf /var/backups/perf
install -d -o root -g perf -m 750 /etc/perf
install -m 755 /tmp/perfd /usr/local/bin/perfd

echo "== 2. токен приёма (генерируется один раз) =="
if [ ! -f /root/lab-perf-token ]; then
    openssl rand -hex 24 > /root/lab-perf-token
    chmod 600 /root/lab-perf-token
fi
TOKEN=$(cat /root/lab-perf-token)
TOKEN_SHA=$(printf '%s' "$TOKEN" | sha256sum | cut -d' ' -f1)

cat > /etc/perf/perfd.env <<ENV
PERF_ADDR=127.0.0.1:8080
PERF_DB=/var/lib/perf/perf.db
PERF_BASE_PATH=/perf
PERF_MAX_BODY=262144
PERF_SCHEMA=1
PERF_FORMULA_VERSION=1
PERF_DEFAULT_BASE=ven-8327
PERF_INGEST_TOKEN_SHA256=$TOKEN_SHA
PERF_LOG_LEVEL=info
ENV
chown root:perf /etc/perf/perfd.env
chmod 640 /etc/perf/perfd.env

echo "== 3. systemd unit =="
install -m 644 /tmp/perf-deploy/perfd.service /etc/systemd/system/perfd.service
systemctl daemon-reload
systemctl enable --now perfd
sleep 2
systemctl is-active perfd

echo "== 4. nginx: сниппет + сайт =="
rm -f /etc/nginx/sites-enabled/default
install -m 644 /tmp/perf-deploy/perf-portal.conf /etc/nginx/snippets/perf-portal.conf
install -m 644 /tmp/perf-deploy/site-lab.conf /etc/nginx/sites-available/testib.ru
ln -sfn /etc/nginx/sites-available/testib.ru /etc/nginx/sites-enabled/testib.ru
nginx -t
systemctl reload nginx

echo "== 5. проверки =="
echo -n "perfd /healthz: "; curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/healthz
echo -n "nginx /perf/api/v1/meta: "; curl -s -H 'Host: testib.ru' http://127.0.0.1/perf/api/v1/meta | head -c 200; echo
echo -n "nginx /healthz: "; curl -s http://127.0.0.1/healthz
echo -n "nginx /nginx-healthz: "; curl -s http://127.0.0.1/nginx-healthz
echo -n "страница /perf/: "; curl -s -o /dev/null -w 'HTTP %{http_code}, %{size_download} байт\n' -H 'Host: testib.ru' http://127.0.0.1/perf/
echo -n "HTTPS 443: "; curl -sk -o /dev/null -w '%{http_code}\n' https://172.28.0.10/perf/api/v1/meta
echo -n "RSS perfd: "; systemctl show perfd -p MemoryCurrent --value | awk '{printf "%.1f МБ\n", $1/1048576}'
echo "токен приёма: /root/lab-perf-token (600)"
