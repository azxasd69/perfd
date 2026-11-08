#!/usr/bin/env bash
# WebDAV-каталог, aliases для postfix, новый обходчик с разбивкой по каналам.
set -euo pipefail

echo "== 1. каталог временных файлов WebDAV =="
install -d -o perf -g perf -m 775 /var/lib/perf/tmp

echo "== 2. aliases (иначе smtpd отвечает 451 Temporary lookup failure) =="
[ -f /etc/aliases ] || install -m 644 /etc/aliases.dpkg-dist /etc/aliases 2>/dev/null || printf 'postmaster: root\n' > /etc/aliases
newaliases
ls -l /etc/aliases /etc/aliases.db

echo "== 3. nginx: /dav/ =="
install -m 644 /tmp/site-lab.conf /etc/nginx/sites-available/testib.ru
nginx -t && systemctl reload nginx

echo "== 4. новый обходчик (канал по подкаталогу) =="
install -m 755 /tmp/perfd /usr/local/bin/perfd
systemctl restart app-perfd app-perfd-watch app-perfd-mail app-perfd-dns
sleep 2
journalctl -u app-perfd-watch -n 2 --no-pager -o cat

echo "== 5. postfix ==="
systemctl restart postfix
ss -lnt | grep -E ':(465|587) '
