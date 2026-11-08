#!/usr/bin/env bash
# Сбор диагностики по каналам 5б/6/7/8.
set -u
echo "=== 1. shadow perfdrop (есть ли хеш) ==="
getent shadow perfdrop | cut -d: -f1,2 | sed 's/\(:.\{6\}\).*/\1…/'
echo "shell: $(getent passwd perfdrop | cut -d: -f7)"

echo "=== 2. sshd по ключу для perfdrop ==="
grep -i 'perfdrop' /var/log/auth.log 2>/dev/null | tail -5 || echo "нет записей"
ls -ld /var/lib/perf/sftp /var/lib/perf/sftp/.ssh /var/lib/perf/sftp/.ssh/authorized_keys
ls -ld /var/lib/perf/sftp/in

echo "=== 3. dovecot ==="
tail -15 /var/log/perf/dovecot.log 2>/dev/null || journalctl -u dovecot -n 10 --no-pager -o cat
ls -l /etc/perf/mail/users
head -c 40 /etc/perf/mail/users; echo

echo "=== 4. postfix ==="
journalctl -u postfix@- -n 15 --no-pager -o cat 2>/dev/null | tail -15
postconf -n | grep -E 'sasl|tls|inet_interfaces|submission' || true
ls -l /var/spool/postfix/private/auth 2>/dev/null || echo "SASL-сокета нет"

echo "=== 5. DNS ==="
dig +tcp @172.28.0.10 probe.t.perftest.testib.ru TXT 2>/dev/null | head -12 || echo "dig недоступен"
journalctl -u app-perfd-dns -n 5 --no-pager -o cat

echo "=== 6. каналы по API ==="
curl -s http://127.0.0.1:8080/perf/healthz/channels; echo
