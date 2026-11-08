#!/usr/bin/env bash
# П-2 плана web-контура (§ 4Б): приведение тестового контура Hyper-V к среде VDS.
# Эталон — снимок Э-0 (tools\vps\backup\20261108) и фактический сервер 91.188.222.193.
# Идемпотентно: повторный запуск не ломает уже настроенное.
set -euo pipefail

export DEBIAN_FRONTEND=noninteractive

echo "== 1. время и пакеты =="
timedatectl set-timezone Europe/Moscow
apt-get update -qq
apt-get install -y -qq nginx certbot fail2ban sqlite3 jq rsync git curl ufw >/dev/null

echo "== 2. unattended-upgrades: без автоперезагрузки (как на VDS) =="
if [ -f /etc/apt/apt.conf.d/50unattended-upgrades ]; then
  sed -i 's|//Unattended-Upgrade::Automatic-Reboot "false";|Unattended-Upgrade::Automatic-Reboot "false";|' \
    /etc/apt/apt.conf.d/50unattended-upgrades || true
fi

echo "== 3. пользователь deploy + узкий sudo (копия правил VDS) =="
if ! id deploy >/dev/null 2>&1; then
  adduser --disabled-password --gecos "" deploy >/dev/null
  usermod -aG www-data deploy
  install -d -m 700 -o deploy -g deploy /home/deploy/.ssh
  if [ -f /home/perfadmin/.ssh/authorized_keys ]; then
    cp /home/perfadmin/.ssh/authorized_keys /home/deploy/.ssh/authorized_keys
    chown deploy:deploy /home/deploy/.ssh/authorized_keys
    chmod 600 /home/deploy/.ssh/authorized_keys
  fi
fi
cat > /etc/sudoers.d/90-deploy <<'SUDOERS'
# deploy: только перезапуск приложений и reload nginx (без произвольного root)
deploy ALL=(root) NOPASSWD: /bin/systemctl restart app-*, /bin/systemctl stop app-*, /bin/systemctl start app-*, /bin/systemctl status app-*, /bin/systemctl reload nginx, /bin/systemctl restart nginx, /usr/sbin/nginx -t, /usr/bin/nginx -t
SUDOERS
chmod 0440 /etc/sudoers.d/90-deploy
visudo -c -f /etc/sudoers.d/90-deploy >/dev/null

echo "== 4. системный пользователь perf и каталоги (как на VDS) =="
id perf >/dev/null 2>&1 || adduser --system --group --home /var/lib/perf --no-create-home perf >/dev/null
install -d -o perf -g perf -m 750 /var/lib/perf /var/log/perf /var/backups/perf
install -d -o root -g perf -m 750 /etc/perf
install -d -o deploy -g www-data -m 775 /var/www/sites/testib.ru/releases
chown deploy:www-data /var/www/sites/testib.ru
chmod 775 /var/www/sites/testib.ru
install -d -o root -g www-data -m 775 /var/www/sites/testib.ru/logs
if [ ! -e /var/www/sites/testib.ru/current ]; then
  ln -sfn /var/www/sites/testib.ru/releases /var/www/sites/testib.ru/current
fi

echo "== 5. ufw: 22/80/443 (как на VDS) =="
ufw --force reset >/dev/null 2>&1 || true
ufw default deny incoming >/dev/null
ufw default allow outgoing >/dev/null
ufw allow 22/tcp >/dev/null
ufw allow 80/tcp >/dev/null
ufw allow 443/tcp >/dev/null
ufw --force enable >/dev/null

echo "== 6. отчёт о состоянии =="
echo -n "nginx: "; nginx -v 2>&1
echo -n "sqlite3: "; sqlite3 --version | cut -d' ' -f1
echo -n "fail2ban: "; fail2ban-server --version | head -1
echo -n "systemd: "; systemctl is-system-running || true
echo -n "службы: "; systemctl is-active nginx fail2ban | tr '\n' ' '; echo
echo "пользователи:"; getent passwd deploy perf
echo "каталоги:"; ls -ld /var/lib/perf /etc/perf /var/log/perf /var/backups/perf /var/www/sites/testib.ru/releases /var/www/sites/testib.ru/logs
echo "ufw:"; ufw status | tail -n +3
