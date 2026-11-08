#!/usr/bin/env bash
# Перевыпуск CA и серверного сертификата контура с корректными расширениями.
# Причина: старый CA (basicConstraints CA:TRUE без keyUsage) отвергается OpenSSL 3 —
# а 1С проверяет цепочку через ЗащищенноеСоединениеOpenSSL (OpenSSL, не SChannel).
# Запуск от root. Старые файлы копируются в /etc/ssl/perf/backup-<дата>/.
set -euo pipefail

DIR=/etc/ssl/perf
BACKUP="$DIR/backup-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$BACKUP"
for f in ca.crt ca.key lab.crt lab.key; do
    [ -f "$DIR/$f" ] && cp -p "$DIR/$f" "$BACKUP/"
done
echo "бэкап: $BACKUP"

cd "$DIR"

# CA: keyUsage обязателен для OpenSSL 3 (RFC 5280, проверка strict).
openssl req -x509 -newkey rsa:2048 -nodes -sha256 -days 825 \
    -keyout ca.key -out ca.crt \
    -subj "/CN=PerfTest Lab CA/O=PerfTest Lab" \
    -addext "basicConstraints=critical,CA:TRUE" \
    -addext "keyUsage=critical,keyCertSign,cRLSign" \
    -addext "subjectKeyIdentifier=hash" 2>/dev/null

# Серверный сертификат: те же SAN, что были (домен и оба IP).
cat > ext.cnf <<'CNF'
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectKeyIdentifier=hash
authorityKeyIdentifier=keyid,issuer
subjectAltName=DNS:testib.ru,DNS:www.testib.ru,IP:127.0.0.1,IP:172.28.0.10
CNF

openssl req -newkey rsa:2048 -nodes -sha256 -keyout lab.key -out lab.csr \
    -subj "/CN=testib.ru/O=PerfTest Lab" 2>/dev/null
openssl x509 -req -in lab.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
    -out lab.crt -days 825 -sha256 -extfile ext.cnf 2>/dev/null

chmod 600 ca.key lab.key
chmod 644 ca.crt lab.crt

echo "== новый CA =="
openssl x509 -in ca.crt -noout -subject -dates -fingerprint -sha256
echo "== проверка цепочки =="
openssl verify -CAfile ca.crt lab.crt
echo "== SAN серверного =="
openssl x509 -in lab.crt -noout -ext subjectAltName

# Копия CA для переноса на хост (читаемая).
install -m 644 ca.crt /tmp/lab-ca.crt

systemctl reload nginx
echo "nginx перезагружен"
