#!/usr/bin/env bash
# П-5: проверки, доступные только в контуре (TLS, отказ БД, fail2ban). Запуск от root.
set -u

echo "=== 1. TLS: параметры протокола ==="
printf 'TLS1.3: '; echo | openssl s_client -connect 172.28.0.10:443 -servername testib.ru -tls1_3 2>/dev/null | grep -m1 'Protocol\|Cipher is' | tr -d '\n'; echo
printf 'TLS1.2: '; echo | openssl s_client -connect 172.28.0.10:443 -servername testib.ru -tls1_2 2>/dev/null | grep -m1 'Cipher is' | tr -d '\n'; echo
printf 'TLS1.1 (ожидается отказ): '; echo | openssl s_client -connect 172.28.0.10:443 -servername testib.ru -tls1_1 2>&1 | grep -m1 -i 'error\|no protocols' | cut -c1-70; echo
printf 'проверка цепочки нашим CA: '; echo | openssl s_client -connect 172.28.0.10:443 -servername testib.ru -CAfile /etc/ssl/perf/ca.crt -verify_return_error 2>&1 | grep -m1 'Verify return code'
printf 'HSTS: '; curl -skI https://172.28.0.10/healthz | grep -i strict-transport | tr -d '\r'

echo "=== 2. Отказ БД -> 503 ==="
printf 'до: '; curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1/healthz
chmod 000 /var/lib/perf/perf.db
sleep 1
printf 'после chmod 000: '; curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1/healthz
chmod 640 /var/lib/perf/perf.db
chown perf:perf /var/lib/perf/perf.db
sleep 1
printf 'после восстановления: '; curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1/healthz

echo "=== 3. fail2ban: состояние и фильтр ==="
systemctl is-active fail2ban
fail2ban-client status sshd 2>&1 | sed -n '1,6p'
echo "--- проверка фильтра на синтетической строке (без реального бана) ---"
fail2ban-regex '2026-11-08T06:20:00.000000+03:00 perf-test sshd[1234]: Failed password for invalid user test from 198.51.100.7 port 40000 ssh2' /etc/fail2ban/filter.d/sshd.conf 2>&1 | grep -E 'Failregex:|matches' | head -3
echo "--- механика бана/разбана на тестовом адресе ---"
fail2ban-client set sshd banip 198.51.100.7 >/dev/null 2>&1
fail2ban-client status sshd | grep -i 'banned ip' | tr -d '\r'
fail2ban-client set sshd unbanip 198.51.100.7 >/dev/null 2>&1
fail2ban-client status sshd | grep -i 'banned ip' | tr -d '\r'

echo "=== 4. Лимиты ресурсов ==="
printf 'perfd MemoryMax: '; systemctl show perfd -p MemoryMax --value | awk '{printf "%.0f МБ\n", $1/1048576}'
printf 'perfd RSS: '; ps -o rss= -C perfd | awk '{printf "%.1f МБ\n", $1/1024}'
printf 'CPU quota: '; systemctl show perfd -p CPUQuotaPerSecUSec --value
