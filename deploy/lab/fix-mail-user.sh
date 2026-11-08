#!/usr/bin/env bash
# Поллер возвращается на пользователя perf (владелец БД) с группой perfmail.
set -euo pipefail
sed -i \
    -e 's/^User=perfing$/User=perf/' \
    -e 's/^Group=perfmail$/Group=perf/' \
    -e 's/^SupplementaryGroups=.*/SupplementaryGroups=perfmail/' \
    /etc/systemd/system/app-perfd-mail.service
grep -E '^(User|Group|SupplementaryGroups)=' /etc/systemd/system/app-perfd-mail.service

chmod 660 /var/lib/perf/perf.db /var/lib/perf/perf.db-wal /var/lib/perf/perf.db-shm 2>/dev/null || true

systemctl daemon-reload
systemctl restart app-perfd-mail
sleep 3
systemctl is-active app-perfd-mail
journalctl -u app-perfd-mail -n 3 --no-pager -o cat | tail -3
