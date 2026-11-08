#!/usr/bin/env bash
# Развёртывание транспортов каналов 5б (FTPS), 6 (SMTP/IMAP), 7 (SFTP) и юнитов app-perfd-*.
# Запуск от root в тестовом контуре. Идемпотентен: повторный прогон не ломает настройки.
# Ожидает в /tmp: vsftpd.conf, postfix-main.cf, postfix-master.cf, dovecot-99-perf.conf,
#                  sshd-99-perf.conf, app-perfd*.service, perfd (бинарник)
set -euo pipefail

echo "== 1. пакеты =="
export DEBIAN_FRONTEND=noninteractive
if ! dpkg -s vsftpd >/dev/null 2>&1 || ! dpkg -s dovecot-imapd >/dev/null 2>&1; then
    echo "postfix postfix/main_mailer_type select No configuration" | debconf-set-selections
    echo "postfix postfix/mailname string testib.ru" | debconf-set-selections
    apt-get update -qq
    apt-get install -y -qq vsftpd postfix dovecot-imapd >/dev/null
fi
echo "vsftpd: $(dpkg-query -W -f='${Version}' vsftpd)"
echo "postfix: $(dpkg-query -W -f='${Version}' postfix)"
echo "dovecot: $(dpkg-query -W -f='${Version}' dovecot-imapd)"

echo "== 2. учётные записи =="
id perfdrop >/dev/null 2>&1 || useradd --system --home /var/lib/perf/sftp --shell /usr/sbin/nologin perfdrop
id perfing  >/dev/null 2>&1 || useradd --system --home /var/mailvhost/perfing --shell /usr/sbin/nologin perfing
id perfack  >/dev/null 2>&1 || useradd --system --home /var/mailvhost/perfack --shell /usr/sbin/nologin perfack
usermod -aG perf perfdrop
usermod -aG perf perfing
usermod -aG perf perfack

echo "== 3. каталоги =="
install -d -o root -g root -m 755 /var/lib/perf/sftp
install -d -o perf -g perf -m 775 /var/lib/perf/in /var/lib/perf/out /var/lib/perf/archive /var/lib/perf/quarantine
for u in perfing perfack; do
    install -d -o "$u" -g "$u" -m 770 "/var/mailvhost/$u"
    for sub in new cur tmp; do
        install -d -o "$u" -g "$u" -m 770 "/var/mailvhost/$u/$sub"
    done
done
install -d -o root -g perf -m 750 /etc/perf/tls /etc/perf/mail
install -m 644 /etc/ssl/perf/lab.crt /etc/perf/tls/lab.crt
install -m 640 /etc/ssl/perf/lab.key /etc/perf/tls/lab.key
chgrp perf /etc/perf/tls/lab.key
install -d -o perf -g perf -m 750 /var/log/perf

echo "== 4. bind-монт in/ внутрь chroot (канал 7) =="
if ! grep -q '/var/lib/perf/sftp/in' /etc/fstab; then
    echo '/var/lib/perf/in  /var/lib/perf/sftp/in  none  bind,rw  0 0' >> /etc/fstab
fi
mkdir -p /var/lib/perf/sftp/in
if ! mountpoint -q /var/lib/perf/sftp/in; then
    mount --bind /var/lib/perf/in /var/lib/perf/sftp/in
fi
mountpoint /var/lib/perf/sftp/in

echo "== 5. vsftpd (канал 5б) =="
install -m 644 /tmp/vsftpd.conf /etc/vsftpd.conf
systemctl enable vsftpd >/dev/null 2>&1 || true
systemctl restart vsftpd
sleep 1
echo -n "слушает 21: "; ss -lntp | grep -c ':21 ' || true

echo "== 6. postfix (канал 6, только submission) =="
cp -a /etc/postfix/main.cf "/etc/postfix/main.cf.bak-$(date +%Y%m%d)" 2>/dev/null || true
cp -a /etc/postfix/master.cf "/etc/postfix/master.cf.bak-$(date +%Y%m%d)" 2>/dev/null || true
install -m 644 /tmp/postfix-main.cf /etc/postfix/main.cf
install -m 644 /tmp/postfix-master.cf /etc/postfix/master.cf
postfix check
systemctl enable postfix >/dev/null 2>&1 || true
systemctl restart postfix
sleep 1

echo "== 7. dovecot (канал 6) =="
install -m 644 /tmp/dovecot-99-perf.conf /etc/dovecot/conf.d/99-perf.conf
if [ ! -s /etc/perf/mail/users ]; then
    : > /root/lab-mail-passwords
    chmod 600 /root/lab-mail-passwords
    : > /etc/perf/mail/users
    for u in perfing perfack; do
        pass=$(openssl rand -hex 12)
        hash=$(doveadm pw -s SHA512-CRYPT -p "$pass")
        uid=$(id -u "$u"); gid=$(id -g "$u")
        echo "$u:$hash:$uid:$gid::$HOME::$SHELL" \
            | sed -e "s|\$HOME|/var/mailvhost/$u|" -e "s|\$SHELL|/usr/sbin/nologin|" >> /etc/perf/mail/users
        echo "$u $pass" >> /root/lab-mail-passwords
    done
fi
chown root:dovecot /etc/perf/mail/users
chmod 640 /etc/perf/mail/users
systemctl enable dovecot >/dev/null 2>&1 || true
systemctl restart dovecot
sleep 1
echo -n "слушает 993: "; ss -lntp | grep -c ':993 ' || true

echo "== 8. sshd (канал 7) =="
install -m 644 /tmp/sshd-99-perf.conf /etc/ssh/sshd_config.d/99-perf.conf
if sshd -t; then
    systemctl reload ssh 2>/dev/null || systemctl reload sshd 2>/dev/null || true
    echo "sshd перечитал конфигурацию"
else
    echo "ОШИБКА: sshd -t не прошёл, конфиг откатан"
    rm -f /etc/ssh/sshd_config.d/99-perf.conf
    exit 1
fi

echo "== 9. ufw: порты каналов =="
for rule in "21/tcp" "990/tcp" "30000:30010/tcp" "587/tcp" "465/tcp" "993/tcp" "53/tcp"; do
    ufw allow "$rule" >/dev/null 2>&1 || true
done
ufw status | head -20

echo "== 10. юниты app-perfd-* =="
install -m 755 /tmp/perfd /usr/local/bin/perfd

# Переменные каналов: добиваем только отсутствующие, токен и прочее не трогаем.
add_env() { grep -q "^$1=" /etc/perf/perfd.env || echo "$1=$2" >> /etc/perf/perfd.env; }
add_env PERF_MAILDIR /var/mailvhost
add_env PERF_DNS_LISTEN 172.28.0.10:53
add_env PERF_DNS_ZONE t.perftest.testib.ru
add_env PERF_CHANNELS http,webdav,ftps,smtp,sftp,dns
add_env PERF_MAX_FILE 5242880
for unit in app-perfd app-perfd-watch app-perfd-mail app-perfd-dns app-perfd-alerts; do
    install -m 644 "/tmp/$unit.service" "/etc/systemd/system/$unit.service"
done
systemctl daemon-reload
# Старый perfd.service заменяется новым именем (sudo у deploy — только на app-*).
systemctl disable --now perfd.service >/dev/null 2>&1 || true
rm -f /etc/systemd/system/perfd.service
systemctl daemon-reload
systemctl enable --now app-perfd app-perfd-watch
sleep 2
systemctl --no-pager --lines=0 status app-perfd app-perfd-watch | sed 's/^/  /'
echo
echo "== 11. итог =="
ss -lntp | grep -E ':(21|990|587|465|993|30000|8080|53) ' || true
echo -n "app-perfd healthz: "; curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/perf/healthz
echo "пароли почтовых учёток: /root/lab-mail-passwords (600)"
