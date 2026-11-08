#!/usr/bin/env bash
# Паритет контура с VDS: пакеты, fail2ban-jail, uid deploy=1000, swap 1750 МБ.
# Идемпотентно. Запуск от root.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

echo "== 1. пакеты как на VDS =="
apt-get update -qq
apt-get install -y -qq python3-certbot-nginx build-essential python3-venv python3-pip \
  zip unzip ncdu tree net-tools >/dev/null
echo "установлено: python3-certbot-nginx build-essential python3-venv python3-pip zip unzip ncdu tree net-tools"

echo "== 2. Node.js 24 (NodeSource), как на VDS =="
if ! command -v node >/dev/null 2>&1; then
  if [ ! -f /etc/apt/sources.list.d/nodesource.list ]; then
    curl -fsSL https://deb.nodesource.com/setup_24.x -o /tmp/nodesource_setup.sh
    bash /tmp/nodesource_setup.sh >/dev/null 2>&1
    rm -f /tmp/nodesource_setup.sh
  fi
  apt-get install -y -qq nodejs >/dev/null
fi
echo -n "node: "; node -v
echo -n "npm: "; npm -v
echo -n "corepack: "; corepack --version 2>/dev/null || echo "-"

echo "== 3. fail2ban jail sshd.local (копия правил VDS) =="
cat > /etc/fail2ban/jail.d/sshd.local <<'JAIL'
[DEFAULT]
bantime  = 1h
findtime = 10m
maxretry = 5

[sshd]
enabled = true
port    = ssh
backend = systemd
JAIL
systemctl restart fail2ban
sleep 2
echo -n "bantime: "; fail2ban-client get sshd bantime
echo -n "maxretry: "; fail2ban-client get sshd maxretry

echo "== 4. uid deploy = 1000 (как на VDS) =="
if [ "$(id -u deploy)" != "1000" ]; then
  # освободить 1000: техническая учётка установщика уезжает на 1500
  if [ "$(id -u perfadmin)" = "1000" ]; then
    usermod -u 1500 perfadmin
    chown -R perfadmin:perfadmin /home/perfadmin
  fi
  pkill -u deploy 2>/dev/null || true
  usermod -u 1000 deploy
  groupmod -g 1000 deploy 2>/dev/null || true
  chown -R deploy:deploy /home/deploy
  find /var/www/sites -user 1001 -exec chown -h deploy:www-data {} + 2>/dev/null || true
fi
echo -n "deploy: "; id deploy
echo -n "perfadmin: "; id perfadmin

echo "== 5. swap 1750 МБ (как на VDS) =="
cur=$(swapon --show=SIZE --noheadings --bytes | head -1 | tr -d ' ')
target=$((1750 * 1024 * 1024))
if [ -z "$cur" ] || [ "$cur" -ne "$target" ]; then
  swapoff /swap.img 2>/dev/null || true
  rm -f /swap.img
  fallocate -l 1750M /swap.img
  chmod 600 /swap.img
  mkswap /swap.img >/dev/null
  swapon /swap.img
  grep -q '^/swap.img' /etc/fstab || echo '/swap.img none swap sw 0 0' >> /etc/fstab
fi
swapon --show

echo "== 6. итог =="
echo -n "nginx: "; nginx -v 2>&1
echo -n "certbot plugin: "; dpkg-query -W -f='${Version}\n' python3-certbot-nginx
echo -n "node: "; node -v
echo -n "gcc: "; gcc --version | head -1 | awk '{print $NF}'
echo -n "swap: "; free -m | awk '/^Swap:/{print $2" МБ"}'
