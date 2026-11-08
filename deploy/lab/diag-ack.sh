#!/usr/bin/env bash
# Что лежит в подтверждениях perfack и почему smtp-конверты не приняты.
set -u
echo "--- подтверждения:"
for f in /var/mailvhost/perfack/Maildir/new/*; do
    [ -e "$f" ] || continue
    echo "== $(basename "$f")"
    sed -n '/^{/,$p' "$f" | head -3
done

echo "--- лог поллера (последние 8):"
journalctl -u app-perfd-mail -n 8 --no-pager -o cat | tail -8

echo "--- письма в cur у perfing:"
ls -1 /var/mailvhost/perfing/Maildir/cur | tail -3
for f in /var/mailvhost/perfing/Maildir/cur/*; do
    [ -e "$f" ] || continue
    echo "== $(basename "$f")"
    grep -i -E '^(Content-Type|Content-Disposition)' "$f" | head -4
    break
done
