#!/usr/bin/env bash
# Финальная настройка каналов: разведение файловых каталогов по транспортам,
# WebDAV в nginx, chroot postfix, доступ dovecot к файлу пользователей.
set -euo pipefail

echo "== 1. доступ dovecot к файлу пользователей =="
chmod 755 /etc/perf/mail
ls -ld /etc/perf/mail

echo "== 2. каталоги по транспортам =="
install -d -o perf -g perf -m 775 /var/lib/perf/in/webdav /var/lib/perf/in/ftps /var/lib/perf/in/sftp
# FTPS: внутри chroot vsftpd (домашний каталог perfdrop)
install -d -o root -g root -m 755 /var/lib/perf/sftp
if ! mountpoint -q /var/lib/perf/sftp/in; then
    mount --bind /var/lib/perf/in/ftps /var/lib/perf/sftp/in
fi
# SFTP: внутри /srv/perf-sftp
if ! mountpoint -q /srv/perf-sftp/in; then
    mount --bind /var/lib/perf/in/sftp /srv/perf-sftp/in
fi
sed -i '\|/var/lib/perf/in  /srv/perf-sftp/in|d' /etc/fstab
grep -q '/var/lib/perf/in/ftps' /etc/fstab || echo '/var/lib/perf/in/ftps  /var/lib/perf/sftp/in  none  bind,rw  0 0' >> /etc/fstab
grep -q '/var/lib/perf/in/sftp' /etc/fstab || echo '/var/lib/perf/in/sftp  /srv/perf-sftp/in  none  bind,rw  0 0' >> /etc/fstab
mountpoint /var/lib/perf/sftp/in; mountpoint /srv/perf-sftp/in
ls -ld /var/lib/perf/sftp/in /srv/perf-sftp/in

echo "== 3. WebDAV: пароль и маршрут nginx =="
if [ ! -f /etc/perf/dav.htpasswd ]; then
    pass=$(awk '/perfdrop/{print $2}' /root/lab-mail-passwords 2>/dev/null || openssl rand -hex 12)
    printf 'perfdrop:%s\n' "$(openssl passwd -apr1 "$(cat /root/lab-ftp-password)")" > /etc/perf/dav.htpasswd
    chmod 640 /etc/perf/dav.htpasswd; chown root:www-data /etc/perf/dav.htpasswd
fi
install -m 644 /tmp/site-lab.conf /etc/nginx/sites-available/testib.ru
nginx -t && systemctl reload nginx

echo "== 4. postfix: chroot выключен у submission =="
install -m 644 /tmp/postfix-master.cf /etc/postfix/master.cf
postfix check
systemctl restart postfix

echo "== 5. перезапуск perfd и обходчика =="
install -m 755 /tmp/perfd /usr/local/bin/perfd
systemctl restart app-perfd app-perfd-watch app-perfd-mail app-perfd-dns
sleep 2
for u in app-perfd app-perfd-watch app-perfd-mail app-perfd-dns app-perfd-alerts; do
    printf '%-20s %s\n' "$u" "$(systemctl is-active $u 2>/dev/null || echo '—')"
done

echo "== 6. живая проверка SMTP и IMAP =="
python3 - "$(awk '/perfing/{print $2}' /root/lab-mail-passwords)" <<'PY'
import smtplib, ssl, sys
pw = sys.argv[1]
ctx = ssl.create_default_context()
ctx.check_hostname = False
ctx.verify_mode = ssl.CERT_NONE
try:
    with smtplib.SMTP("172.28.0.10", 587, timeout=15) as s:
        s.set_debuglevel(0)
        s.starttls(context=ctx)
        s.login("perfing", pw)
        print("SMTP STARTTLS + SASL: ok")
except Exception as e:
    print("SMTP:", type(e).__name__, e)
PY
