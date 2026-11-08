# Тестовый TLS для контура: свой CA + сертификат на testib.ru.
# Нужен потому, что Let's Encrypt в лаборатории недоступен (нет публичного имени).
# Кладёт файлы в /etc/ssl/perf: ca.crt, lab.crt/lab.key (от CA), self.crt/self.key (самоподписанный).
set -euo pipefail

DIR=/etc/ssl/perf
install -d -m 0750 "$DIR"
cd "$DIR"

if [ -f ca.crt ] && [ -f lab.crt ] && [ -f self.crt ]; then
    echo "сертификаты уже созданы"
else
    openssl req -x509 -newkey rsa:2048 -sha256 -days 730 -nodes \
        -keyout ca.key -out ca.crt -subj "/CN=PerfTest Lab CA" >/dev/null 2>&1

    openssl req -newkey rsa:2048 -nodes -keyout lab.key -out lab.csr \
        -subj "/CN=testib.ru" >/dev/null 2>&1

    cat > ext.cnf <<'EXT'
subjectAltName=DNS:testib.ru,DNS:www.testib.ru,IP:127.0.0.1,IP:172.28.0.10
basicConstraints=CA:FALSE
keyUsage=digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
EXT

    openssl x509 -req -in lab.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
        -out lab.crt -days 730 -sha256 -extfile ext.cnf >/dev/null 2>&1

    # Самоподписанный вариант — для проверки деградации клиента до уровня B/C (КПР § 8.3).
    openssl req -x509 -newkey rsa:2048 -sha256 -days 730 -nodes \
        -keyout self.key -out self.crt -subj "/CN=testib.ru" \
        -addext "subjectAltName=DNS:testib.ru,IP:172.28.0.10" >/dev/null 2>&1

    chmod 0640 lab.key self.key ca.key
    echo "сертификаты созданы"
fi

echo -n "CA: "; openssl x509 -in ca.crt -noout -subject
echo -n "server: "; openssl x509 -in lab.crt -noout -subject
echo -n "server SAN: "; openssl x509 -in lab.crt -noout -ext subjectAltName | tail -1
