#!/usr/bin/env bash
# Поллер читает письма как их владелец (perfing) и пишет в БД через группу perf.
set -euo pipefail

chmod 775 /var/lib/perf
chgrp perf /var/lib/perf
chmod 660 /var/lib/perf/perf.db
[ -f /var/lib/perf/perf.db-wal ] && chmod 660 /var/lib/perf/perf.db-wal
[ -f /var/lib/perf/perf.db-shm ] && chmod 660 /var/lib/perf/perf.db-shm
ls -ld /var/lib/perf; ls -l /var/lib/perf/perf.db*

sed -i \
    -e 's/^User=perf$/User=perfing/' \
    -e 's/^Group=perf$/Group=perfmail/' \
    -e 's/^SupplementaryGroups=.*/SupplementaryGroups=perf/' \
    /etc/systemd/system/app-perfd-mail.service
grep -E '^(User|Group|SupplementaryGroups)=' /etc/systemd/system/app-perfd-mail.service

systemctl daemon-reload
systemctl restart app-perfd-mail
sleep 12
systemctl is-active app-perfd-mail
journalctl -u app-perfd-mail --since '-1 min' --no-pager -o cat | grep -E 'письмо|ERROR' | tail -4
sqlite3 /var/lib/perf/perf.db "select channel, count(*) from ingest_log group by channel order by 2 desc;"
