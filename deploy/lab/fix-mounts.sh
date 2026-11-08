#!/usr/bin/env bash
# Перемонтирование bind-точек по транспортам (старые указывали на in/ целиком).
set -euo pipefail

for mp in /var/lib/perf/sftp/in /srv/perf-sftp/in; do
    if mountpoint -q "$mp"; then
        umount "$mp"
        echo "размонтирован $mp"
    fi
done

install -d -o root -g root -m 755 /var/lib/perf/sftp /srv/perf-sftp
mount --bind /var/lib/perf/in/ftps /var/lib/perf/sftp/in
mount --bind /var/lib/perf/in/sftp /srv/perf-sftp/in

echo "--- точки монтирования:"
findmnt -n -o TARGET,SOURCE /var/lib/perf/sftp/in /srv/perf-sftp/in
echo "--- содержимое (должно быть пусто после перемонтирования):"
ls -la /var/lib/perf/sftp/in /srv/perf-sftp/in | head -12

echo "--- fstab (актуальные строки):"
grep -E 'perf/in|perf-sftp' /etc/fstab || true
