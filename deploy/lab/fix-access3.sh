#!/usr/bin/env bash
# Доступ postfix к TLS-ключу и полный список каналов в окружении.
set -eu
chmod 755 /etc/perf/tls
ls -ld /etc/perf/tls; ls -l /etc/perf/tls/
sudo -u postfix test -r /etc/perf/tls/lab.key && echo "postfix читает ключ" || echo "postfix НЕ читает"

sed -i 's|^PERF_CHANNELS=.*|PERF_CHANNELS=http,webdav,ftps,smtp,sftp,dns|' /etc/perf/perfd.env
grep -E '^PERF_CHANNELS|^PERF_MAILDIR|^PERF_DNS' /etc/perf/perfd.env

systemctl restart app-perfd
sleep 2
curl -s http://127.0.0.1:8080/perf/healthz/channels; echo
