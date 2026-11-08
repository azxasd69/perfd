#!/usr/bin/env bash
# Согласование Maildir: dovecot смотрит в <u>/Maildir (как кладёт postfix),
# поллер получает доступ к БД через группу perf.
set -euo pipefail

echo "== 1. Maildir для perfack (ack-письма) =="
install -d -o perfack -g perfmail -m 2770 /var/mailvhost/perfack/Maildir
for sub in new cur tmp; do
    install -d -o perfack -g perfmail -m 2770 "/var/mailvhost/perfack/Maildir/$sub"
done

echo "== 2. dovecot: mail_location -> <u>/Maildir =="
sed -i 's|^mail_location = .*|mail_location = maildir:/var/mailvhost/%u/Maildir|' /etc/dovecot/conf.d/99-perf.conf
grep '^mail_location' /etc/dovecot/conf.d/99-perf.conf
systemctl restart dovecot
sleep 1
systemctl is-active dovecot

echo "== 3. поллер: доступ к БД через группу perf =="
usermod -aG perf perfing
chown perf:perf /var/lib/perf/perf.db
chmod 660 /var/lib/perf/perf.db
ls -l /var/lib/perf/perf.db

echo "== 4. перезапуск поллера =="
sed -i 's/^SupplementaryGroups=.*/SupplementaryGroups=perfmail perf/' /etc/systemd/system/app-perfd-mail.service
systemctl daemon-reload
systemctl restart app-perfd-mail
sleep 4
systemctl is-active app-perfd-mail
journalctl -u app-perfd-mail -n 4 --no-pager -o cat | tail -4

echo "== 5. отправка тестового письма =="
pass=$(awk '/perfing/{print $2}' /root/lab-mail-passwords)
python3 - "$pass" <<'PY'
import smtplib, ssl, sys, json, hashlib, secrets, time
from email.message import EmailMessage

pw = sys.argv[1]
env = {"schema": 1, "id": "r-" + secrets.token_urlsafe(32)[:43], "kind": "result",
       "stand": {"key": "стенд-smtp", "fingerprint": hashlib.sha256(b"lab").hexdigest(),
                 "platform": "8.3.27.2342"},
       "measured_at_utc": int(time.time()),
       "results": [{"scenario": "CPU_Коллекции", "median_ms": 30.0, "per_unit_ms": 300.0,
                    "unit": "операций", "portion": 100, "repeats": 2, "min_ms": 29.0, "max_ms": 31.0}]}
body = json.dumps({"client": {"app": "PerfTest", "version": "1.22"}, "batch": [env]},
                  ensure_ascii=False).encode()

msg = EmailMessage()
msg["From"] = "perfing@testib.ru"
msg["To"] = "perfing@testib.ru"
msg["Subject"] = "perf check " + env["id"]
msg.set_content("конверт во вложении")
msg.add_attachment(body, maintype="application", subtype="json", filename=env["id"] + ".json")

ctx = ssl.create_default_context(); ctx.check_hostname = False; ctx.verify_mode = ssl.CERT_NONE
with smtplib.SMTP("172.28.0.10", 587, timeout=20) as s:
    s.starttls(context=ctx)
    s.login("perfing", pw)
    s.send_message(msg)
print("отправлено:", env["id"])
open("/tmp/last-mail-id", "w").write(env["id"])
PY

sleep 15
echo "== 6. результат =="
sqlite3 /var/lib/perf/perf.db "select channel, status, count(*) from ingest_log group by channel, status order by 3 desc;"
echo -n "письма в perfack: "; ls -1 /var/mailvhost/perfack/Maildir/new 2>/dev/null | wc -l
journalctl -u app-perfd-mail -n 3 --no-pager -o cat | tail -3
