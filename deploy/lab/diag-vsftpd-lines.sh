#!/usr/bin/env bash
# Проверяет каждую строку /etc/vsftpd.conf отдельно: какие параметры сборка не принимает.
set -u
base='listen=YES
listen_ipv6=NO
anonymous_enable=NO
local_enable=YES'

grep -v '^#' /etc/vsftpd.conf | grep -v '^$' | while IFS= read -r line; do
    case "$line" in
        listen=*|listen_ipv6=*|anonymous_enable=*|local_enable=*) continue ;;
    esac
    printf '%s\n%s\n' "$base" "$line" > /tmp/t.conf
    timeout 1 /usr/sbin/vsftpd /tmp/t.conf >/tmp/o 2>&1
    rc=$?
    if [ "$rc" -eq 2 ]; then
        echo "ОТВЕРГНУТ: $line"
    fi
    pkill -f '/usr/sbin/vsftpd /tmp/t.conf' 2>/dev/null
    sleep 0.2
done
echo "проверка завершена"
