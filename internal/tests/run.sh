#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "${script_dir}/../.." && pwd)"
packages_file="${script_dir}/packages.txt"

packages=()
while IFS= read -r line || [ -n "$line" ]; do
	line="${line%%#*}"
	line="$(printf '%s' "$line" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')"
	if [ -n "$line" ]; then
		packages+=("$line")
	fi
done < "$packages_file"

if [ "${#packages[@]}" -eq 0 ]; then
	echo "no test packages configured in ${packages_file}" >&2
	exit 1
fi

cd "$repo_root"
go test "${packages[@]}" "$@"
