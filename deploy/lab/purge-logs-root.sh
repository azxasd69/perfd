#!/usr/bin/env bash
# Очистка системных журналов тестового контура. Запускать ТОЛЬКО под root.
#
# Почему отдельным скриптом: сервис perfd работает пользователем perf с
# NoNewPrivileges=true и ProtectSystem=strict, поэтому ни nginx-логи (www-data),
# ни dovecot.log (root), ни журнал systemd ему недоступны. Кнопка «Очистить данные»
# в интерфейсе чистит базу, файлы обмена и счётчики; системные журналы — здесь.
#
#   ./purge-logs-root.sh              # nginx + /var/log/perf
#   ./purge-logs-root.sh --with-journal   # плюс journald (ВНИМАНИЕ: журнал общий для всей системы)
set -euo pipefail

WITH_JOURNAL=0
[ "${1:-}" = "--with-journal" ] && WITH_JOURNAL=1

echo "== nginx =="
for f in /var/www/sites/testib.ru/logs/access.log /var/www/sites/testib.ru/logs/error.log; do
    if [ -f "$f" ]; then
        before=$(stat -c%s "$f")
        : > "$f"
        echo "обнулён $f ($before Б)"
    fi
done

echo "== /var/log/perf =="
shopt -s nullglob
for f in /var/log/perf/*.log; do
    before=$(stat -c%s "$f")
    : > "$f"
    echo "обнулён $f ($before Б)"
done
shopt -u nullglob

if [ "$WITH_JOURNAL" = "1" ]; then
    echo "== journald (общий журнал системы) =="
    journalctl --rotate >/dev/null
    journalctl --vacuum-time=1s >/dev/null 2>&1 || true
    echo "журнал systemd сокращён"
else
    echo "== journald пропущен (нужен флаг --with-journal) =="
fi

echo "готово"
