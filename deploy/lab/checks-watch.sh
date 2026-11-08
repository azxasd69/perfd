#!/usr/bin/env bash
# Проверка файлового канала (WebDAV/FTPS/SFTP): кладём конверт в in/,
# ждём подтверждение в out/, сверяем журнал приёма. Запуск от root.
set -euo pipefail

IN=/var/lib/perf/in
OUT=/var/lib/perf/out
ARCH=/var/lib/perf/archive

rm -f "$IN"/*.json "$OUT"/*.ack "$OUT"/*.err 2>/dev/null || true

ID="r-$(openssl rand -hex 22 | tr -d '\n' | cut -c1-43)"
NOW=$(date +%s)
FP=$(printf 'webdav-test' | sha256sum | cut -d' ' -f1)

cat > "$IN/$ID.json" <<JSON
{"client":{"app":"PerfTest","version":"1.22"},
 "batch":[{"schema":1,"id":"$ID","kind":"result",
   "stand":{"key":"webdav-стенд","fingerprint":"$FP","platform":"8.3.27.2342"},
   "measured_at_utc":$NOW,
   "results":[{"scenario":"IO_Чтение","representation":"Чтение файла","median_ms":25.0,
               "per_unit_ms":250.0,"unit":"операций","portion":100,"repeats":2,
               "min_ms":24.0,"max_ms":27.0}]}]}
JSON
chown perf:perf "$IN/$ID.json"

echo "== файл положен: $ID =="
timeout 90 sudo -u perf env PERF_DB=/var/lib/perf/perf.db PERF_BASE_PATH=/perf PERF_LOG_LEVEL=info \
    /usr/local/bin/perfd -watch -in "$IN" -out "$OUT" -archive "$ARCH" -channel webdav >/tmp/watch.log 2>&1 &
WATCH_PID=$!
sleep 45
kill $WATCH_PID 2>/dev/null || true
wait $WATCH_PID 2>/dev/null || true

echo "== подтверждение =="
ls -l "$OUT" | tail -5
cat "$OUT/$ID.ack" 2>/dev/null || echo "НЕТ ПОДТВЕРЖДЕНИЯ"

echo "== архив =="
find "$ARCH" -name "$ID.json" | tail -1

echo "== журнал приёма =="
sqlite3 /var/lib/perf/perf.db "select channel, status, id from ingest_log where channel='webdav' order by received_at desc limit 5;"

echo "== стенд из файла =="
sqlite3 /var/lib/perf/perf.db "select st.id, g.id, r.scenario, r.per_unit_ms from stands st join stand_generations g on g.stand_id=st.id join results r on r.generation_id=g.id where st.id like 'webdav%';"

echo "== каналы по API =="
curl -s http://127.0.0.1:8080/perf/healthz/channels
echo
echo "== лог обходчика =="
tail -5 /tmp/watch.log
