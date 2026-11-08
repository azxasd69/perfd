#!/usr/bin/env bash
# Диагностика запуска vsftpd: вывод в stdout/stderr вместо syslog.
set -x
ls -l /etc/perf/tls/
/usr/sbin/vsftpd -v 2>&1 | head -3
timeout 3 /usr/sbin/vsftpd /etc/vsftpd.conf
echo "rc=$?"
ss -lnt | grep ':21 ' || echo "порт 21 не слушается"
echo "--- последние строки /var/log/syslog:"
tail -5 /var/log/syslog 2>/dev/null || echo "нет syslog"
echo "--- проверка конфигурации через -olisten=NO:"
timeout 3 /usr/sbin/vsftpd -olisten=NO -olisten_ipv6=NO /etc/vsftpd.conf
echo "rc2=$?"
