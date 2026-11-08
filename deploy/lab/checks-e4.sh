#!/usr/bin/env bash
# Проверки Э-4 на контуре: миграция идемпотентна, страницы отдаются,
# экспорт -> распаковка -> -check (восстановление сходится), retention без потерь.
set -euo pipefail

echo "== 1. повторный запуск службы не сдвигает схему =="
systemctl restart perfd
sleep 2
echo -n "schema: "; sqlite3 /var/lib/perf/perf.db 'select max(version) from schema_meta;'
systemctl is-active perfd

echo "== 2. страницы по HTTPS =="
for p in /perf/ /perf/stands /perf/top /perf/about /perf/stands/webdav-стенд; do
    printf '%-34s ' "$p"
    curl -sk -o /tmp/page.html -w 'HTTP %{http_code} %{size_download} байт\n' --get --data-urlencode "" "https://127.0.0.1$p"
done
echo -n "заголовок дашборда: "; curl -sk https://127.0.0.1/perf/ | grep -o '<h1>[^<]*</h1>' | head -1
echo -n "карточка стенда без токена (private): "; curl -sk -o /dev/null -w '%{http_code}\n' https://127.0.0.1/perf/stands/webdav-стенд

echo "== 3. экспорт =="
rm -f /tmp/perf-snap.db.gz /tmp/perf-snap.db.gz.manifest.json
sudo -u perf env PERF_DB=/var/lib/perf/perf.db /usr/local/bin/perfd -export /var/lib/perf/perf-snap.db.gz
ls -l /var/lib/perf/perf-snap.db.gz*
cat /var/lib/perf/perf-snap.db.gz.manifest.json

echo "== 4. восстановление и -check =="
rm -rf /tmp/restore && mkdir -p /tmp/restore
gunzip -c /var/lib/perf/perf-snap.db.gz > /tmp/restore/perf.db
chown -R perf:perf /tmp/restore; chmod 750 /tmp/restore
echo -n "исходная база:  "; sudo -u perf env PERF_DB=/var/lib/perf/perf.db /usr/local/bin/perfd -check
echo -n "восстановленная: "; sudo -u perf env PERF_DB=/tmp/restore/perf.db /usr/local/bin/perfd -check

echo "== 5. retention (свёртывать нечего — окно не истекло) =="
sudo -u perf env PERF_DB=/var/lib/perf/perf.db PERF_RETAIN_MONTHS=12 /usr/local/bin/perfd -retention

echo "== 6. размеры =="
ls -l /var/lib/perf/perf.db
echo -n "RSS: "; systemctl show perfd -p MemoryCurrent --value | awk '{printf "%.1f МБ\n", $1/1048576}'
echo -n "свободно на диске: "; df -h / | tail -1
