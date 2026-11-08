#!/usr/bin/env bash
# master.cf: штатный набор служб + submission/submissions, 25 не слушается.
# Причина: самодельный минимальный master.cf не содержал proxymap, и smtpd
# зависал на первом же запросе (STARTTLS таймаутил).
set -euo pipefail

cp -a /usr/share/postfix/master.cf.dist /etc/postfix/master.cf

# Гарантия: 25 не слушается (строка smtp в dist закомментирована, проверяем).
sed -i 's/^smtp\([[:space:]]\+inet\)/#smtp\1/' /etc/postfix/master.cf

cat >> /etc/postfix/master.cf <<'EOF'

# --- канал 6: только submission с SASL через dovecot -------------------------
# chroot выключен (пятая колонка = n): в chroot нет /etc/perf/tls, TLS не поднимался.
submission inet n       -       n       -       -       smtpd
  -o syslog_name=postfix/submission
  -o smtpd_tls_security_level=encrypt
  -o smtpd_sasl_auth_enable=yes
  -o smtpd_client_restrictions=permit_sasl_authenticated,reject
submissions inet n      -       n       -       -       smtpd
  -o syslog_name=postfix/submissions
  -o smtpd_tls_security_level=encrypt
  -o smtpd_sasl_auth_enable=yes
  -o smtpd_client_restrictions=permit_sasl_authenticated,reject
EOF

postconf -M | grep -c '^smtp ' && echo "ВНИМАНИЕ: 25 слушается" || echo "25 не слушается"
postfix check
systemctl restart postfix
sleep 2
ss -lnt | grep -E ':(25|465|587) ' || true

echo "== проверка STARTTLS =="
timeout 12 openssl s_client -starttls smtp -connect 172.28.0.10:587 -brief 2>&1 | head -8
