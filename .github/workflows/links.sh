#!/bin/sh
# Fail if any relative markdown link points at something that is not there.
# A dead link in a README is the first thing a new reader hits.
set -eu
status=0
for f in $(git ls-files '*.md'); do
	dir=$(dirname "$f")
	grep -oE '\]\([^)#][^)]*\)' "$f" 2>/dev/null | sed 's/^](//;s/)$//' | while read -r link; do
		case "$link" in
			http://*|https://*|mailto:*) continue ;;
		esac
		target="${link%%#*}"
		[ -z "$target" ] && continue
		case "$target" in
			/*) path=".$target" ;;
			*)  path="$dir/$target" ;;
		esac
		if [ ! -e "$path" ]; then
			echo "dead link: $f -> $link"
			exit 1
		fi
	done || status=1
done
[ "$status" -eq 0 ] && echo "all relative links resolve"
exit "$status"
