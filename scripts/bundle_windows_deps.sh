#!/usr/bin/env bash
# Resolve every DLL a Windows payload needs, copy the non-system ones in, and
# fail if any dependency cannot be accounted for.
#
# The zip used to name its DLLs by hand, which shipped a build that died on
# startup with "libopus-0.dll was not found": the exe had picked up a mingw DLL
# nobody had thought to copy. Walking the import tables instead means the
# payload is derived from what the binaries actually ask for.
#
# Usage: bundle_windows_deps.sh <payload-dir> [search-dir ...]
#
# Every PE already in <payload-dir> is a root of the walk. Imports are resolved
# against <payload-dir> first, then the search directories (whose DLLs get
# copied in), then the Windows system directory (whose DLLs are left alone,
# since they are part of the OS).
set -euo pipefail

PAYLOAD="${1:-}"
if [ -z "$PAYLOAD" ] || [ ! -d "$PAYLOAD" ]; then
    echo "usage: $0 <payload-dir> [search-dir ...]" >&2
    exit 2
fi
shift

SEARCH_DIRS=("$@")
if [ ${#SEARCH_DIRS[@]} -eq 0 ]; then
    SEARCH_DIRS=(/mingw64/bin)
fi

# Where the OS keeps its own DLLs. Anything found here is assumed present on the
# target machine and is not bundled.
SYSTEM_DIR="${DAS_SYSTEM_DIR:-}"
if [ -z "$SYSTEM_DIR" ] && [ -n "${SYSTEMROOT:-}" ] && command -v cygpath >/dev/null 2>&1; then
    SYSTEM_DIR="$(cygpath "$SYSTEMROOT")/System32"
fi

# DLL names are case-insensitive to the Windows loader, and import tables spell
# them inconsistently (KERNEL32.dll, libdave.dll). Match the same way.
find_in() {
    local dir="$1" name="$2"
    [ -n "$dir" ] && [ -d "$dir" ] || return 1
    local hit
    hit="$(find "$dir" -maxdepth 1 -iname "$name" -print -quit 2>/dev/null)"
    [ -n "$hit" ] || return 1
    printf '%s\n' "$hit"
}

imports_of() {
    objdump -p "$1" 2>/dev/null | awk '/DLL Name:/ {print $NF}'
}

seen=" "
queue=()
missing=()
bundled=()

for pe in "$PAYLOAD"/*; do
    case "${pe,,}" in
    *.exe | *.dll) queue+=("$pe") ;;
    esac
done

if [ ${#queue[@]} -eq 0 ]; then
    echo "Error: no .exe or .dll found in $PAYLOAD" >&2
    exit 1
fi

if [ -z "$SYSTEM_DIR" ] || [ ! -d "$SYSTEM_DIR" ]; then
    echo "Warning: no Windows system directory to check against; OS DLLs will" >&2
    echo "         be reported as missing. Set DAS_SYSTEM_DIR to fix." >&2
fi

while [ ${#queue[@]} -gt 0 ]; do
    current="${queue[0]}"
    queue=("${queue[@]:1}")

    while read -r dep; do
        [ -n "$dep" ] || continue
        key="${dep,,}"
        case "$seen" in *" $key "*) continue ;; esac
        seen+="$key "

        # API sets are not files the way ordinary DLLs are: the loader
        # resolves these names through the schema baked into Windows itself.
        # msvcp140.dll imports a dozen of them and none can be bundled.
        case "$key" in
        api-ms-win-* | ext-ms-win-*)
            echo "   api-set   $dep"
            continue
            ;;
        esac

        if path="$(find_in "$PAYLOAD" "$dep")"; then
            echo "   bundled  $dep"
            queue+=("$path")
            continue
        fi

        found=""
        for dir in "${SEARCH_DIRS[@]}"; do
            if path="$(find_in "$dir" "$dep")"; then
                cp "$path" "$PAYLOAD/"
                echo "-> copied   $dep (from $dir)"
                bundled+=("$dep")
                queue+=("$PAYLOAD/$(basename "$path")")
                found=1
                break
            fi
        done
        [ -n "$found" ] && continue

        if find_in "$SYSTEM_DIR" "$dep" >/dev/null; then
            echo "   system    $dep"
            continue
        fi

        echo "!! MISSING   $dep (needed by $(basename "$current"))"
        missing+=("$dep")
    done < <(imports_of "$current")
done

if [ ${#missing[@]} -gt 0 ]; then
    echo >&2
    echo "Error: ${#missing[@]} dependency/dependencies could not be resolved:" >&2
    printf '  %s\n' "${missing[@]}" >&2
    echo "The payload would fail to start on a clean machine." >&2
    exit 1
fi

echo "-> all dependencies resolved (${#bundled[@]} copied in)"
