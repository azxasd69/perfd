#!/usr/bin/env bash
# Куда легли файлы и письма: сверка ingest_log, каталогов и Maildir.
set -u
echo "--- ingest по каналам:"
sqlite3 /var/lib/perf/perf.db "select channel, count(*) from ingest_log group by channel order by 2 desc;"
echo "--- последние 5 записей:"
sqlite3 /var/lib/perf/perf.db "select channel, status, substr(id,1,18), received_at from ingest_log order by received_at desc limit 5;"

echo "--- in/:"
find /var/lib/perf/in -maxdepth 2 -name '*.json' -printf '%p\n' 2>/dev/null | head
echo "--- out/:"
ls -1 /var/lib/perf/out | tail -5
echo "--- archive (сегодня):"
ls -1 /var/lib/perf/archive/$(date +%F) 2>/dev/null | tail -5

echo "--- Maildir perfing:"
ls -la /var/mailvhost/perfing/new /var/mailvhost/perfing/cur 2>/dev/null | head -12
echo "--- Maildir perfack:"
ls -la /var/mailvhost/perfack/new 2>/dev/null | tail -5
echo "--- mail log, последние строки про perfing:"
grep -i 'perfing' /var/log/mail.log | tail -5 || echo "нет записей"
echo "--- mail-поллер:"
journalctl -u app-perfd-mail -n 6 --no-pager -o cat
