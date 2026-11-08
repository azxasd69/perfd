#!/usr/bin/env bash
# Адрес машины для проверки портов + обновление бинарника.
set -euo pipefail
add_env() { grep -q "^$1=" /etc/perf/perfd.env || echo "$1=$2" >> /etc/perf/perfd.env; }
add_env PERF_STATUS_HOST 172.28.0.10
grep -E '^PERF_STATUS_HOST' /etc/perf/perfd.env
install -m 755 /tmp/perfd /usr/local/bin/perfd
systemctl restart app-perfd
sleep 2
systemctl is-active app-perfd
echo "--- status:"
curl -s http://127.0.0.1:8080/perf/api/v1/status | head -c 900; echo
