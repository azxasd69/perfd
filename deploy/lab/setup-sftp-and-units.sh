#!/usr/bin/env bash
# Ключ для канала 7 (SFTP) и запуск юнитов app-perfd-mail / app-perfd-dns.
set -euo pipefail

echo "== ключ perfdrop =="
if [ ! -f /root/perfdrop_key ]; then
    ssh-keygen -t ed25519 -N '' -C 'perfdrop@lab' -f /root/perfdrop_key >/dev/null
fi
install -d -o perfdrop -g perfdrop -m 700 /var/lib/perf/sftp/.ssh
install -m 600 -o perfdrop -g perfdrop /root/perfdrop_key.pub /var/lib/perf/sftp/.ssh/authorized_keys
chown perfdrop:perfdrop /var/lib/perf/sftp/.ssh/authorized_keys

echo "== юниты -mail и -dns =="
systemctl daemon-reload
systemctl enable --now app-perfd-mail app-perfd-dns
sleep 2
for u in app-perfd-mail app-perfd-dns; do
    printf '%-18s %s\n' "$u" "$(systemctl is-active $u)"
    journalctl -u "$u" -n 3 --no-pager -o cat | sed 's/^/    /'
done
echo "== слушатели =="
ss -lntp | grep -E ':(53|993|587|465|21|22|8080) ' || true
echo "== закрытый ключ для проверки с хоста =="
base64 -w0 /root/perfdrop_key; echo
