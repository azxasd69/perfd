#!/usr/bin/env bash
# Проверки контура после установки perfd (П-3/П-4). Запускать от root.
set -u

install -m 644 /tmp/perf-deploy/perf-portal.conf /etc/nginx/snippets/perf-portal.conf
nginx -t 2>&1 | tail -1
systemctl reload nginx

echo "--- проверки ---"
printf 'healthz:        '; curl -s http://127.0.0.1/healthz
printf 'nginx-healthz:  '; curl -s http://127.0.0.1/nginx-healthz
printf 'perf/healthz:   '; curl -s http://127.0.0.1/perf/healthz
printf 'meta:           '; curl -s -H 'Host: testib.ru' http://127.0.0.1/perf/api/v1/meta | head -c 130; echo
printf 'page /perf/:    '; curl -s -o /dev/null -w '%{http_code}, %{size_download} байт\n' -H 'Host: testib.ru' http://127.0.0.1/perf/
printf 'metrics:        '; curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1/perf/metrics
printf 'https 443:      '; curl -sk -o /dev/null -w '%{http_code}\n' https://172.28.0.10/perf/api/v1/meta
printf 'rss perfd:      '; ps -o rss= -C perfd | awk '{printf "%.1f МБ\n", $1/1024}'
printf 'perfd active:   '; systemctl is-active perfd
printf 'ufw:            '; ufw status | sed -n '3p'
