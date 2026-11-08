#!/usr/bin/env bash
# Точное перемонтирование с проверкой маркерами.
set -u
for mp in /var/lib/perf/sftp/in /srv/perf-sftp/in; do
    mountpoint -q "$mp" && umount "$mp"
done
mkdir -p /var/lib/perf/sftp/in /srv/perf-sftp/in
mount --bind /var/lib/perf/in/ftps /var/lib/perf/sftp/in; echo "mount ftps rc=$?"
mount --bind /var/lib/perf/in/sftp /srv/perf-sftp/in; echo "mount sftp rc=$?"

echo "--- маркеры:"
touch /var/lib/perf/in/ftps/.probe-ftps
touch /var/lib/perf/in/sftp/.probe-sftp
ls -a /var/lib/perf/sftp/in /srv/perf-sftp/in
rm -f /var/lib/perf/in/ftps/.probe-ftps /var/lib/perf/in/sftp/.probe-sftp

echo "--- монтирования:"
grep -E 'perf-sftp|perf/sftp' /proc/mounts
