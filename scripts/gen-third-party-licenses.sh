#!/usr/bin/env bash
# Copyright (C) 2026 Franklin D. Amador
#
# This software is dual-licensed under the terms of the GPL v3.0 and
# a commercial license. You may choose to use this software under either
# license.
#
# See the LICENSE files in the project root for full license text.

# Writes the license texts of every module linked into the current Go module's
# packages. Run it from a module root after `go mod download`:
#
#	scripts/gen-third-party-licenses.sh                       # THIRD_PARTY_LICENSES.txt
#	scripts/gen-third-party-licenses.sh -tags sqlite -o THIRD_PARTY_LICENSES-sqlite.txt
#	(cd examples/guard/field && ../../../scripts/gen-third-party-licenses.sh)
set -euo pipefail

tags=""
out=THIRD_PARTY_LICENSES.txt
while [[ $# -gt 0 ]]; do
	case $1 in
	-tags) tags=$2; shift 2 ;;
	-o) out=$2; shift 2 ;;
	*) echo "usage: $0 [-tags TAGS] [-o FILE]" >&2; exit 2 ;;
	esac
done

self=$(go list -m)
rule=$(printf '%.0s-' {1..72})
bar=$(printf '%.0s=' {1..72})

# Repository of a module path.
url() {
	case $1 in
	charm.land/*) local n=${1#charm.land/}; echo "https://github.com/charmbracelet/${n%%/*}" ;;
	github.com/apache/plc4x/plc4go) echo "https://github.com/apache/plc4x (plc4go)" ;;
	github.com/*)
		local owner repo rest
		IFS=/ read -r _ owner repo rest <<<"$1"
		if [[ -n $rest && ! $rest =~ ^v[0-9]+$ ]]; then
			echo "https://github.com/$owner/$repo ($rest)"
		else
			echo "https://github.com/$owner/$repo"
		fi ;;
	golang.org/x/*) echo "https://go.googlesource.com/${1#golang.org/x/}" ;;
	modernc.org/*) echo "https://gitlab.com/cznic/${1#modernc.org/}" ;;
	*) echo "https://$1" ;;
	esac
}

# License name from a license text.
kind() {
	if grep -q "dual-licensed" "$1"; then
		if grep -q "version 2" "$1"; then echo "Dual license: GPLv2 or later, or commercial"
		else echo "Dual license: GPLv3, or commercial"; fi
	elif grep -q "Apache License" "$1"; then echo "Apache License 2.0"
	elif grep -q "Permission is hereby granted" "$1"; then echo "MIT License"
	elif grep -qi "Neither the name" "$1"; then echo "BSD 3-Clause License"
	elif grep -q "Redistribution and use" "$1"; then echo "BSD 2-Clause License"
	else echo "See license text"
	fi
}

{
	echo "$bar"
	echo "THIRD-PARTY SOFTWARE NOTICES AND ACKNOWLEDGMENTS"
	echo "$bar"
	echo
	echo "$self uses the software components below. The required notices and"
	echo "licenses for these components are reproduced here. Each component"
	echo "remains under its own license."
	echo
	echo "Generated $(date +%Y-%m-%d) by scripts/gen-third-party-licenses.sh${tags:+ with -tags $tags}"
	echo "from the modules linked into $self. See also NOTICE.md."
	echo

	n=0
	go list ${tags:+-tags "$tags"} -deps -f '{{with .Module}}{{.Path}}{{end}}' ./... |
		sort -u | { grep -v -x -e '' -e "$self" || true; } |
		while read -r path; do
			IFS='|' read -r version dir replaced < <(go list -m -f '{{.Version}}|{{if .Replace}}{{.Replace.Dir}}|yes{{else}}{{.Dir}}|{{end}}' "$path")
			# A nested module (e.g. honeycomb/connectors/plc4x) may rely on its
			# repository's license, so look up to three directories higher.
			for _ in 1 2 3 4; do
				mapfile -t files < <(cd "$dir" && find . -maxdepth 2 -type f \
					\( -iname 'LICENSE*' -o -iname 'COPYING*' -o -iname 'NOTICE*' -o -name 'AUTHORS' \) \
					! -iname 'LICENSE-LOGO' ! -path './tools/*' | sed 's|^\./||' | sort)
				[[ ${#files[@]} -gt 0 ]] && break
				dir=$(dirname "$dir")
			done
			[[ ${#files[@]} -gt 0 ]] || { echo "no license file for $path" >&2; exit 1; }
			main=$dir/${files[0]}
			for f in "${files[@]}"; do [[ $f == LICENSE || $f == LICENSE.md || $f == LICENSE.txt ]] && main=$dir/$f; done
			if [[ -n $replaced ]]; then
				version="development checkout ($(git -C "$dir" log -1 --format='%h, %cd' --date=short 2>/dev/null || echo local))"
			fi
			n=$((n + 1))
			echo "$rule"
			echo "$n. Component Name: $path"
			echo "   Version: $version"
			echo "   Project URL: $(url "$path")"
			echo "   License Type: $(kind "$main")"
			echo "$rule"
			for f in "${files[@]}"; do
				[[ ${#files[@]} -gt 1 ]] && { echo "[$f]"; echo; }
				tr -d '\r' <"$dir/$f"
				echo
			done
			if grep -q "dual-licensed" "$main"; then
				echo
				echo "The full GPL text is in the gpl-*.md file of the $path repository."
			fi
			echo
		done

	echo "$bar"
	echo "END OF THIRD-PARTY SOFTWARE NOTICES"
	echo "$bar"
} >"$out"

echo "wrote $out"
