#!/usr/bin/env bash

set -euo pipefail

if ! command -v go-licenses >/dev/null 2>&1; then
	printf '%s\n' 'ERROR: go-licenses not found; run inside casjaysdev/go:latest' >&2
	exit 69
fi

printf '%s\n' 'Scanning Go dependency licenses...'
# Only report dependencies imported by the distributed server and CLI binaries.
scan_packages=(./src ./src/client)
license_report=$(go-licenses csv "${scan_packages[@]}")
module_list=$(go list -m all)
declare -A license_overrides=(
	[github.com/alibabacloud-go/darabonba-array/client]='github.com/alibabacloud-go/darabonba-array|v0.1.0|Apache-2.0|https://docs.c3.ai/docs/platform/8.9/topic/C3OperatorThirdParty'
	[github.com/alibabacloud-go/darabonba-encode-util/client]='github.com/alibabacloud-go/darabonba-encode-util|v0.0.2|MIT|https://docs.c3.ai/docs/platform/8.9/topic/C3OperatorThirdParty'
	[github.com/alibabacloud-go/darabonba-map/client]='github.com/alibabacloud-go/darabonba-map|v0.0.2|MIT|https://docs.c3.ai/docs/platform/8.9/topic/C3OperatorThirdParty'
	[github.com/alibabacloud-go/darabonba-signature-util/client]='github.com/alibabacloud-go/darabonba-signature-util|v0.0.7|MIT|https://docs.c3.ai/docs/platform/8.9/topic/C3OperatorThirdParty'
	[github.com/alibabacloud-go/darabonba-string/client]='github.com/alibabacloud-go/darabonba-string|v1.0.2|Apache-2.0|https://github.com/alibabacloud-go/darabonba-string/blob/v1.0.2/README.md'
	[github.com/alibabacloud-go/endpoint-util/service]='github.com/alibabacloud-go/endpoint-util|v1.1.0|Apache-2.0|https://github.com/alibabacloud-go/endpoint-util/blob/v1.1.0/README.md'
	[modernc.org/mathutil]='modernc.org/mathutil|v1.7.1|BSD-3-Clause|https://gitlab.com/cznic/mathutil/-/blob/v1.7.1/LICENSE'
)
for package in "${!license_overrides[@]}"; do
	override=${license_overrides[$package]}
	module=${override%%|*}
	expected_version=${override#*|}
	expected_version=${expected_version%%|*}
	actual_version=$(printf '%s\n' "$module_list" | awk -v module="$module" '$1 == module { print $2; exit }')
	if [[ "$actual_version" != "$expected_version" ]]; then
		printf 'ERROR: license evidence for %s is pinned to %s, found %s; review and update the attribution.\n' "$module" "$expected_version" "${actual_version:-missing}" >&2
		exit 1
	fi
done

copyleft=$(printf '%s\n' "$license_report" | grep -iE ',(GPL|AGPL|LGPL)(-[^,]+)?$' || true)
if [[ -n "$copyleft" ]]; then
	printf '%s\n' 'ERROR: incompatible copyleft licenses detected:' "$copyleft" >&2
	exit 1
fi
ignored_packages=()
checked_report=''
declare -A seen_overrides=()
while IFS=',' read -r package license_url license_name; do
	if [[ "$license_name" == 'Unknown' ]]; then
		override=${license_overrides[$package]:-}
		if [[ -z "$override" ]]; then
			printf 'ERROR: dependency license could not be identified: %s\n' "$package" >&2
			exit 1
		fi
		license_url=${override##*|}
	license_name=${override%|*}
	license_name=${license_name##*|}
		ignored_packages+=(--ignore="$package")
		seen_overrides[$package]=1
	fi
	checked_report+="$package,$license_url,$license_name"$'\n'
done <<< "$license_report"
for package in "${!license_overrides[@]}"; do
	if [[ -z "${seen_overrides[$package]:-}" ]]; then
		printf 'ERROR: license scanner output changed for %s; review and update the attribution override.\n' "$package" >&2
		exit 1
	fi
done

temp_root="${TMPDIR:-/tmp}/apimgr"
mkdir -p "$temp_root"
license_work=$(mktemp -d "$temp_root/gitmessages-XXXXXX")
chmod 700 "$license_work"
trap 'rm -rf "$license_work"' EXIT
printf '%s' "$checked_report" > "$license_work/licenses.csv"
go-licenses save "${ignored_packages[@]}" "${scan_packages[@]}" --save_path="$license_work/third_party_licenses"

printf '%s\n' 'All dependency licenses are identified and compatible.'
cat "$license_work/licenses.csv"
