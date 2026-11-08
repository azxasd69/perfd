#!/usr/bin/env bash
# Бисекция конфигурации vsftpd: какой параметр роняет запуск.
set -u
cd /tmp
base='listen=YES
listen_ipv6=NO
anonymous_enable=NO
local_enable=YES
write_enable=YES'

try() {
    local name="$1" extra="$2"
    printf '%s\n%s\n' "$base" "$extra" > /tmp/t.conf
    out=$(timeout 2 /usr/sbin/vsftpd /tmp/t.conf 2>&1)
    rc=$?
    printf '%-22s rc=%s %s\n' "$name" "$rc" "${out:0:120}"
    pkill -f '/usr/sbin/vsftpd /tmp/t.conf' 2>/dev/null
    sleep 0.3
}

try "базовый" ""
try "ssl_enable" "ssl_enable=YES
rsa_cert_file=/etc/perf/tls/lab.crt
rsa_private_key_file=/etc/perf/tls/lab.key"
try "+allow_writeable_chroot" "chroot_local_user=YES
allow_writeable_chroot=YES"
try "+tlsv1_3" "ssl_tlsv1_3=YES"
try "+require_ssl_reuse" "require_ssl_reuse=NO"
try "+pasv" "pasv_enable=YES
pasv_min_port=30000
pasv_max_port=30010
pasv_address=172.28.0.10"
try "+xferlog" "xferlog_enable=YES
xferlog_file=/var/log/perf/vsftpd.log"
try "+secure_chroot" "secure_chroot_dir=/var/run/vsftpd/empty"
try "+pam" "pam_service_name=vsftpd"
try "+hide_ids" "hide_ids=YES
ls_recurse_enable=NO"
try "полный" "$(grep -v '^#' /etc/vsftpd.conf | grep -v '^$' | tail -n +9)"
