#!/usr/bin/env bash
# Постоянные bind-монты и юнит оповещений.
set -euo pipefail

echo "== fstab: только актуальные строки =="
sed -i '\|/var/lib/perf/in|d' /etc/fstab
cat >> /etc/fstab <<'EOF'
/var/lib/perf/in/ftps  /var/lib/perf/sftp/in  none  bind,rw  0 0
/var/lib/perf/in/sftp  /srv/perf-sftp/in       none  bind,rw  0 0
EOF
grep -E 'perf/in/(ftps|sftp)' /etc/fstab

echo "== юнит оповещений =="
systemctl enable --now app-perfd-alerts
sleep 2
systemctl is-active app-perfd-alerts
journalctl -u app-perfd-alerts -n 3 --no-pager -o cat | tail -3

echo "== все юниты =="
for u in app-perfd app-perfd-watch app-perfd-mail app-perfd-dns app-perfd-alerts; do
    printf '%-20s %s\n' "$u" "$(systemctl is-active $u)"
done
echo "== память по юнитам =="
systemctl show -p MemoryCurrent app-perfd app-perfd-watch app-perfd-mail app-perfd-dns app-perfd-alerts |
    awk -F= '{printf "%-24s %.1f МБ\n", $1, $2/1048576}'
echo "== суммарно RSS всех служб контура =="
ps -eo rss,comm --sort=-rss | awk 'NR==1 || $2 ~ /perfd|nginx|postfix|dovecot|vsftpd|ssh|named/ {sum+=$1} END {printf "%.0f МБ\n", sum/1024}'
