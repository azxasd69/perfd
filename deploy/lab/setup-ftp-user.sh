#!/usr/bin/env bash
# Пароль учётки perfdrop для FTPS (канал 5б) и перезапуск vsftpd.
set -euo pipefail
install -m 644 /tmp/vsftpd.conf /etc/vsftpd.conf
if [ ! -f /root/lab-ftp-password ]; then
    openssl rand -hex 12 > /root/lab-ftp-password
    chmod 600 /root/lab-ftp-password
fi
echo "perfdrop:$(cat /root/lab-ftp-password)" | chpasswd
systemctl restart vsftpd
sleep 1
systemctl is-active vsftpd
ss -lnt | grep ':21 ' || echo "порт 21 не слушается"
echo -n "пароль: "
cat /root/lab-ftp-password
echo
echo -n "домашний каталог: "
getent passwd perfdrop | cut -d: -f6
