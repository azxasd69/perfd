#!/usr/bin/env bash
# Устранение трёх причин отказов: pam_shells, chroot для SFTP, ключ TLS postfix.
set -euo pipefail

echo "== 1. nologin в списке оболочек (нужно pam_shells для FTP-учётки) =="
grep -qx '/usr/sbin/nologin' /etc/shells || echo '/usr/sbin/nologin' >> /etc/shells
tail -3 /etc/shells

echo "== 2. отдельный chroot для SFTP (sshd требует root:root и без записи группе) =="
install -d -o root -g root -m 755 /srv/perf-sftp
if ! grep -q '/srv/perf-sftp/in' /etc/fstab; then
    echo '/var/lib/perf/in  /srv/perf-sftp/in  none  bind,rw  0 0' >> /etc/fstab
fi
sed -i 's|^/var/lib/perf/in  /var/lib/perf/sftp/in.*$||' /etc/fstab
mkdir -p /srv/perf-sftp/in
if ! mountpoint -q /srv/perf-sftp/in; then
    mount --bind /var/lib/perf/in /srv/perf-sftp/in
fi
mountpoint /srv/perf-sftp/in

echo "== 3. sshd: ChrootDirectory -> /srv/perf-sftp =="
sed -i 's|ChrootDirectory /var/lib/perf/sftp|ChrootDirectory /srv/perf-sftp|' /etc/ssh/sshd_config.d/99-perf.conf
sshd -t && systemctl reload ssh && echo "sshd ok"

echo "== 4. перезапуск служб =="
systemctl restart vsftpd postfix dovecot
sleep 2
for u in vsftpd postfix dovecot; do printf '%-10s %s\n' "$u" "$(systemctl is-active $u)"; done

echo "== 5. проверка TLS-ключа postfix ==="
sudo -u postfix test -r /etc/perf/tls/lab.key && echo "postfix читает ключ" || echo "postfix НЕ читает ключ"
