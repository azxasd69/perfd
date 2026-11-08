#!/usr/bin/env bash
# П-5: отказ БД по-настоящему — эксклюзивная блокировка SQLite.
# /healthz должен отдать 503 за счёт 2-секундного контекста и busy_timeout.
set -u

printf 'до: '
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1/healthz

python3 - <<'PY' &
import sqlite3, time
con = sqlite3.connect('/var/lib/perf/perf.db', timeout=1)
con.execute('BEGIN EXCLUSIVE')
time.sleep(8)
con.rollback()
con.close()
PY
LOCK_PID=$!
sleep 1

printf 'во время блокировки: '
curl -s -o /dev/null -w '%{http_code} (%{time_total}s)\n' --max-time 10 http://127.0.0.1/healthz
printf 'тело ответа: '
curl -s --max-time 10 http://127.0.0.1/healthz

wait $LOCK_PID 2>/dev/null
sleep 1
printf 'после снятия блокировки: '
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1/healthz
printf 'журнал (последние 2 строки): '
journalctl -u perfd -n 2 --no-pager -o cat | tr '\n' ' ' | cut -c1-160; echo
