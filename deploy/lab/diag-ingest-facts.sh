#!/usr/bin/env bash
# Факты по журналу приёма на контуре: идентификаторы пакетов, каналы, размеры.
set -u
DB=/var/lib/perf/perf.db
echo "=== версия схемы и таблицы ==="
sqlite3 $DB "select max(version) from schema_meta;"
sqlite3 $DB ".tables"
echo "=== уникальные request_id и их длина (10 последних) ==="
sqlite3 $DB "select request_id, length(request_id), count(*) from ingest_log group by request_id order by max(received_at) desc limit 10;"
echo "=== записей с префиксом pkt- ==="
sqlite3 $DB "select count(*) from ingest_log where request_id like 'pkt-%';"
echo "=== каналы и статусы ==="
sqlite3 $DB "select channel, status, count(*) from ingest_log group by channel, status order by 3 desc;"
echo "=== bytes: распределение ==="
sqlite3 $DB "select bytes, count(*) from ingest_log group by bytes order by 2 desc limit 5;"
echo "=== есть ли где-нибудь тела пакетов ==="
sqlite3 $DB "select name from sqlite_master where type='table' and (name like '%payload%' or name like '%batch%' or name like '%channel%');"
echo "=== размер БД и число строк истории ==="
ls -l $DB | awk '{print $5" байт"}'
sqlite3 $DB "select count(*) from history; select count(*) from results;"
