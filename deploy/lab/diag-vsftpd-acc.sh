#!/usr/bin/env bash
# Наращивание конфига vsftpd строка за строкой: первая строка, ломающая запуск.
set -u
: > /tmp/acc.conf
grep -v '^#' /etc/vsftpd.conf | grep -v '^$' | while IFS= read -r line; do
    echo "$line" >> /tmp/acc.conf
    timeout 1 /usr/sbin/vsftpd /tmp/acc.conf >/tmp/o 2>&1
    rc=$?
    if [ "$rc" -eq 2 ]; then
        echo "ЛОМАЕТ на строке: $line"
        echo "--- вывод: $(cat /tmp/o)"
        echo "--- накопленный конфиг:"
        cat -n /tmp/acc.conf
        break
    fi
    pkill -f '/usr/sbin/vsftpd /tmp/acc.conf' 2>/dev/null
    sleep 0.2
done
echo "конец"
