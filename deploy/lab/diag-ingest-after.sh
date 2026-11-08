#!/usr/bin/env bash
# Состояние журнала приёма после отрицательных проверок.
set -u
echo -n "строк в ingest_log: "
sqlite3 /var/lib/perf/perf.db "select count(*) from ingest_log;"
echo "по статусам:"
sqlite3 /var/lib/perf/perf.db "select status, count(*) from ingest_log group by status;"
echo "последние 3 request_id:"
sqlite3 /var/lib/perf/perf.db "select request_id, length(request_id), channel, status from ingest_log order by received_at desc limit 3;"
