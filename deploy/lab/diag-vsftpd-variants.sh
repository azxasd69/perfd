#!/usr/bin/env bash
# Варианты полного конфига vsftpd: какой набор строк запускается.
set -u
run() {
    local name="$1" file="$2"
    timeout 2 /usr/sbin/vsftpd "$file" >/tmp/o 2>&1
    local rc=$?
    printf '%-28s rc=%s %s\n' "$name" "$rc" "$(head -c 100 /tmp/o)"
    pkill -f "/usr/sbin/vsftpd $file" 2>/dev/null
    sleep 0.3
}

cp /etc/vsftpd.conf /tmp/v-full.conf
run "полный" /tmp/v-full.conf

grep -v 'check_shell' /tmp/v-full.conf > /tmp/v-nocheckshell.conf
run "без check_shell" /tmp/v-nocheckshell.conf

grep -v 'ssl_sslv' /tmp/v-full.conf > /tmp/v-nosslv.conf
run "без ssl_sslv2/3" /tmp/v-nosslv.conf

grep -v 'ssl_tlsv1=' /tmp/v-full.conf > /tmp/v-notls1.conf
run "без ssl_tlsv1=NO" /tmp/v-notls1.conf

grep -v 'secure_chroot_dir' /tmp/v-full.conf > /tmp/v-nochroot.conf
run "без secure_chroot_dir" /tmp/v-nochroot.conf

grep -vE 'ssl_sslv|ssl_tlsv1=|check_shell' /tmp/v-full.conf > /tmp/v-min.conf
run "без трёх групп" /tmp/v-min.conf
