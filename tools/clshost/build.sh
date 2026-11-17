#!/bin/sh
# Builds clshost.exe, the Windows helper that runs one CLS codec DLL from file to file under Wine (see
# internal/codec/plugin). Needs mingw-w64 (macOS: brew install mingw-w64; Debian: apt install gcc-mingw-w64-i686).
# Output: .build/clshost.exe; point go-unarc at it with UNARC_CLSHOST, or put it beside the go-unarc binary.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
out=$(cd "$here/../.." && pwd)/.build
cc=${CC:-i686-w64-mingw32-gcc}
command -v "$cc" >/dev/null 2>&1 || { echo "clshost: no $cc (install mingw-w64 or set CC)" >&2; exit 2; }
mkdir -p "$out"
"$cc" -O2 -static -Wall -Wl,--large-address-aware -o "$out/clshost.exe" "$here/clshost.c"
echo "$out/clshost.exe"
