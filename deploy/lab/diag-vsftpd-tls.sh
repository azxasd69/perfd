#!/usr/bin/env bash
# Проверка каждого параметра TLS-блока vsftpd 3.0.5 по отдельности (с путями к сертификату).
set -u
C=/etc/perf/tls/lab.crt
K=/etc/perf/tls/lab.key
base="listen=YES
listen_ipv6=NO
local_enable=YES
rsa_cert_file=$C
rsa_private_key_file=$K
ssl_enable=YES"

t() {
    local name="$1" extra="$2"
    printf '%s\n%s\n' "$base" "$extra" > /tmp/tt.conf
    timeout 1 /usr/sbin/vsftpd /tmp/tt.conf >/tmp/o 2>&1
    printf '%-26s rc=%s %s\n' "$name" "$?" "$(head -c 80 /tmp/o)"
    pkill -f '/usr/sbin/vsftpd /tmp/tt.conf' 2>/dev/null
    sleep 0.2
}

t "голый ssl" ""
t "force_logins_ssl" "force_local_logins_ssl=YES"
t "force_data_ssl" "force_local_data_ssl=YES"
t "ssl_tlsv1_2" "ssl_tlsv1_2=YES"
t "ssl_tlsv1=NO" "ssl_tlsv1=NO"
t "ssl_sslv2=NO" "ssl_sslv2=NO"
t "ssl_sslv3=NO" "ssl_sslv3=NO"
t "require_ssl_reuse" "require_ssl_reuse=NO"
t "check_shell" "check_shell=NO"
t "hide_ids" "hide_ids=YES"
t "xferlog" "xferlog_enable=YES
xferlog_file=/var/log/perf/vsftpd.log"
t "pam" "pam_service_name=vsftpd"
