#!/usr/bin/env bash
# Прямой захват вывода vsftpd и проверка сборки.
set -u
echo "--- unit:"
systemctl cat vsftpd | grep -E 'ExecStart|User'
echo "--- сборка (SSL):"
ldd /usr/sbin/vsftpd | grep -i -E 'ssl|crypto' || echo "нет libssl в зависимостях"
strings /usr/sbin/vsftpd | grep -c 'ssl_enable' || true
echo "--- прямой запуск с захватом:"
/usr/sbin/vsftpd -olisten=NO -olisten_ipv6=NO /etc/vsftpd.conf > /tmp/vs.out 2>&1
echo "rc=$?"
echo "вывод: [$(cat /tmp/vs.out)]"
echo "--- то же с ssl_enable=NO:"
/usr/sbin/vsftpd -olisten=NO -olisten_ipv6=NO -ossl_enable=NO /etc/vsftpd.conf > /tmp/vs2.out 2>&1
echo "rc=$?"
echo "вывод: [$(cat /tmp/vs2.out)]"
echo "--- dmesg:"
dmesg 2>/dev/null | tail -3
