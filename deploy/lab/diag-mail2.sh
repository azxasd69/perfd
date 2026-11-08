#!/usr/bin/env bash
# Состояние почтового канала после правок.
set -u
echo "--- лог поллера (2 мин):"
journalctl -u app-perfd-mail --since '-2 min' --no-pager -o cat | grep -E 'письмо|ERROR|запущен' | tail -6
echo "--- каналы:"
sqlite3 /var/lib/perf/perf.db "select channel, status, count(*) from ingest_log group by channel, status order by 3 desc;"
echo "--- письма:"
ls -1 /var/mailvhost/perfing/Maildir/new | wc -l
ls -1 /var/mailvhost/perfack/Maildir/new | wc -l
