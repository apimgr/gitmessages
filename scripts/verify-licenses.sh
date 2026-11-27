#!/usr/bin/env bash

set -euo pipefail

if ! command -v go-licenses >/dev/null 2>&1; then
	printf '%s\n' 'ERROR: go-licenses not found; run inside casjaysdev/go:latest' >&2
	exit 69
fi

printf '%s\n' 'Scanning Go dependency licenses...'
overrides_file=.github/license-overrides.txt
ignore_args=()
override_rows=()
declare -A override_license=()
if [[ -f "$overrides_file" ]]; then
	while read -r module license _reviewer_note; do
		[[ -z "${module:-}" || "$module" == \#* ]] && continue
		if [[ -z "${license:-}" || -z "${_reviewer_note:-}" ]]; then
			printf 'ERROR: malformed license override for %s; expected module, SPDX ID, and review note.\n' "$module" >&2
			exit 1
		fi
		if [[ -n "${override_license[$module]:-}" ]]; then
			printf 'ERROR: duplicate license override for %s.\n' "$module" >&2
			exit 1
		fi
		override_license[$module]=$license
		ignore_args+=(--ignore "$module")
		override_rows+=("$module,$license")
	done < "$overrides_file"
fi

dependencies=$(go list -deps -f '{{.ImportPath}}' ./src/...)
for module in "${!override_license[@]}"; do
	if ! grep -Fxq "$module" <<< "$dependencies" && ! grep -Fq "$module/" <<< "$dependencies"; then
		printf 'ERROR: license override is unused or scanner output changed for %s; review and remove or update it.\n' "$module" >&2
		exit 1
	fi
done
license_report=$(go-licenses csv ./src/... "${ignore_args[@]}")
if grep -iE ',Unknown($|,)' <<< "$license_report"; then
	printf '%s\n' 'ERROR: dependency license could not be identified; add a reviewed override or remove the dependency.' >&2
	exit 1
fi
checked_report="$license_report"$'\n'
for row in "${override_rows[@]}"; do
	checked_report+="$row"$'\n'
done
copyleft=$(printf '%s\n' "$checked_report" | grep -iE ',(GPL|AGPL|LGPL)(-[^,]+)?($|,)' || true)
if [[ -n "$copyleft" ]]; then
	printf '%s\n' 'ERROR: incompatible copyleft licenses detected:' "$copyleft" >&2
	exit 1
fi

temp_root="${TMPDIR:-/tmp}/apimgr/gitmessages"
mkdir -p "$temp_root"
license_work=$(mktemp -d "$temp_root/licenses-XXXXXX")
chmod 700 "$license_work"
trap 'rm -rf "$license_work"' EXIT
printf '%s' "$checked_report" > "$license_work/licenses.csv"
go-licenses save ./src/... "${ignore_args[@]}" --save_path="$license_work/third_party_licenses"

printf '%s\n' 'All dependency licenses are identified and compatible.'
cat "$license_work/licenses.csv"
