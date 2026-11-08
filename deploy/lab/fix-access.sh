#!/usr/bin/env bash
# Доступы для каналов 5б/6/7: /etc/perf, ключ TLS для postfix, PAM и sshd.
set -u
echo "=== /etc/perf: делаем обходимым (файлы остаются 640) ==="
chmod 755 /etc/perf
ls -ld /etc/perf /etc/perf/perfd.env /etc/perf/mail/users

echo "=== TLS-ключ читается postfix ==="
chown root:postfix /etc/perf/tls/lab.key
chmod 640 /etc/perf/tls/lab.key
ls -l /etc/perf/tls/

echo "=== PAM vsftpd ==="
grep -vE '^\s*#|^\s*$' /etc/pam.d/vsftpd

echo "=== sshd для perfdrop ==="
sshd -T -C user=perfdrop,host=localhost,addr=172.28.0.1 2>&1 | grep -E 'chrootdirectory|forcecommand|passwordauthentication|authenticationmethods|subsystem|permittty|allowtcpforwarding' 

echo "=== проверка chroot ==="
namei -l /var/lib/perf/sftp/in 2>/dev/null | head -8
