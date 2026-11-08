#!/usr/bin/env bash
# Какой контент в каждом релизе: где старая демо-страница, где новая.
set -u
for r in $(ls -1 /var/www/sites/testib.ru/releases | tail -5); do
    dir="/var/www/sites/testib.ru/releases/$r"
    printf '%-18s %-9s ' "$r" "$(wc -c < "$dir/index.html" 2>/dev/null)б"
    title=$(grep -o '<title>[^<]*</title>' "$dir/index.html" 2>/dev/null | head -1)
    old=$(grep -c 'Сайт работает с Windows-хоста' "$dir/index.html" 2>/dev/null)
    echo "$title старых совпадений: $old"
done
echo "--- активный релиз:"
readlink -f /var/www/sites/testib.ru/current
