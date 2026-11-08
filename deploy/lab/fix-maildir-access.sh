#!/usr/bin/env bash
# Доступы к Maildir: общая группа perfmail, поллер работает владельцем почты.
set -euo pipefail

getent group perfmail >/dev/null || groupadd perfmail
usermod -aG perfmail perfing
usermod -aG perfmail perfack
usermod -aG perfmail perf

chgrp -R perfmail /var/mailvhost
find /var/mailvhost -type d -exec chmod 2770 {} +
find /var/mailvhost -type f -exec chmod 660 {} +
ls -ld /var/mailvhost/perfing /var/mailvhost/perfing/Maildir /var/mailvhost/perfack

echo "== юнит -mail: User=perfing =="
sed -i \
    -e 's/^User=perf$/User=perfing/' \
    -e 's/^Group=perf$/Group=perfmail/' \
    -e 's/^SupplementaryGroups=perfing perfack$/SupplementaryGroups=perfmail/' \
    /etc/systemd/system/app-perfd-mail.service
grep -E '^(User|Group|SupplementaryGroups)=' /etc/systemd/system/app-perfd-mail.service
systemctl daemon-reload
systemctl restart app-perfd-mail
sleep 3
systemctl is-active app-perfd-mail
journalctl -u app-perfd-mail -n 4 --no-pager -o cat
