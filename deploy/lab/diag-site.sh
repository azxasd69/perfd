#!/usr/bin/env bash
# Что реально отдаётся на 80 и 443 и какой релиз активен.
set -u
echo "=== симлинк current ==="
ls -l /var/www/sites/testib.ru/current
echo "=== релизы ==="
ls -1 /var/www/sites/testib.ru/releases | tail -5
echo "=== файлы активного релиза ==="
ls -l /var/www/sites/testib.ru/current/ | head -10
echo "=== размер и заголовок index.html ==="
wc -c /var/www/sites/testib.ru/current/index.html
grep -o '<title>[^<]*</title>' /var/www/sites/testib.ru/current/index.html || true

echo "=== HTTP 80 (Host: testib.ru) ==="
curl -s -H 'Host: testib.ru' http://127.0.0.1/ | head -c 200; echo
echo "=== HTTPS 443 ==="
curl -sk https://127.0.0.1/ | head -c 200; echo

echo "=== заголовки ответа 80 ==="
curl -sI -H 'Host: testib.ru' http://127.0.0.1/ | head -12
echo "=== корни в конфигурации nginx ==="
nginx -T 2>/dev/null | grep -E '^\s*(root|server_name|listen)' | head -20
