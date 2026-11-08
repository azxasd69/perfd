#!/usr/bin/env bash
# Почему ssl_enable роняет vsftpd: проверка пары ключ/сертификат и минимальных наборов.
set -u
C=/etc/perf/tls/lab.crt
K=/etc/perf/tls/lab.key
echo "--- файлы:"
ls -l "$C" "$K"
echo "--- модули сертификата и ключа:"
openssl x509 -noout -modulus -in "$C" | md5sum
openssl rsa  -noout -modulus -in "$K" | md5sum
echo "--- срок:"
openssl x509 -noout -dates -in "$C"

t() {
    local name="$1"; shift
    timeout 2 /usr/sbin/vsftpd "$@" >/tmp/o 2>&1
    echo "$name rc=$? $(head -c 100 /tmp/o)"
    pkill -f '/usr/sbin/vsftpd' 2>/dev/null
    sleep 0.2
}

printf 'listen=YES\nlisten_ipv6=NO\nlocal_enable=YES\nssl_enable=YES\nrsa_cert_file=%s\nrsa_private_key_file=%s\n' "$C" "$K" > /tmp/s1.conf
t "ssl+cert+key" /tmp/s1.conf

printf 'listen=YES\nlisten_ipv6=NO\nlocal_enable=YES\nssl_enable=YES\n' > /tmp/s2.conf
t "ssl без cert" /tmp/s2.conf

cp /etc/vsftpd.conf /tmp/s3.conf
sed -i 's/^ssl_enable=YES/ssl_enable=NO/' /tmp/s3.conf
t "полный без ssl" /tmp/s3.conf

echo "--- версия пакета vsftpd:"
dpkg-query -W -f='${Version}\n' vsftpd
