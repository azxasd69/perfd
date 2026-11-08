#!/usr/bin/env bash
# Подготовка: вход root по ключу (как на VDS, где ключ root есть) + uid deploy=1000.
# Запуск от root, но менять uid нужно из сессии root: usermod не даёт менять пользователя,
# под которым есть живые процессы.
set -euo pipefail

PUB='ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBkThBsVLNfItBzetwLZWWUsD/j96+dyn7azQZyEE8s4 perf-lab'
install -d -m 700 /root/.ssh
touch /root/.ssh/authorized_keys
chmod 600 /root/.ssh/authorized_keys
grep -qxF "$PUB" /root/.ssh/authorized_keys || echo "$PUB" >> /root/.ssh/authorized_keys
echo "root key: $(wc -l < /root/.ssh/authorized_keys) строк"

echo "== uid =="
echo -n "до: "; id perfadmin; id deploy

# 1) освободить 1000 переводом технической учётки на 1500
if [ "$(id -u perfadmin)" = "1000" ]; then
    usermod -u 1500 perfadmin
    groupmod -g 1500 perfadmin 2>/dev/null || true
    chown -R perfadmin:perfadmin /home/perfadmin 2>/dev/null || true
    echo "perfadmin -> 1500"
fi

# 2) deploy получает 1000/1000, как на VDS
if [ "$(id -u deploy)" != "1000" ]; then
    usermod -u 1000 deploy
    groupmod -g 1000 deploy
    chown -R deploy:deploy /home/deploy
    find /var/www/sites -uid 1001 -exec chown -h deploy:www-data {} + 2>/dev/null || true
    echo "deploy -> 1000"
fi

echo -n "после: "; id deploy
echo -n "perfadmin: "; id perfadmin
ls -ld /home/deploy /home/perfadmin /var/www/sites/testib.ru /var/www/sites/testib.ru/releases
