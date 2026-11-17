#!/bin/sh
# Rebuilds testdata/genuine: archives made by the genuine FreeArc 0.67 and the C unarc's output for them.
#
#   scripts/genuine/build.sh [WORKDIR]
#
# 1. builds the Docker image go-unarc-freearc067 (Dockerfile: an i386 Debian guest with Wine and FreeArc
#    0.67, booted by qemu-system-i386; FreeArc itself is downloaded from the Wayback Machine, checksummed);
# 2. writes the inputs (go run ./scripts/genuine inputs) to WORKDIR/in;
# 3. boots the guest twice to run archives.sh, which writes WORKDIR/out: the "main" set on 1 CPU, the
#    "mt" set (presets FreeArc builds from multi-threaded 4x4 methods) on 4 CPUs;
# 4. copies the archives to testdata/genuine/archives and records the C unarc's l/v/t runs in
#    testdata/genuine/golden, plus testdata/genuine/README.md (go run ./scripts/genuine golden).
#
# Needs Docker, Go, git, the C unarc binary (C_UNARC) and a git checkout of its sources (FREEARC_SRC), whose
# HEAD sources are the text inputs. Takes about 10 minutes on an Apple silicon Mac once the image is built
# (everything i386 is emulated).
set -eu
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
work=${1:-$(mktemp -d "${TMPDIR:-/tmp}/go-unarc-genuine.XXXXXX")}
unarc=${C_UNARC:?set C_UNARC to the C unarc binary}
src=${FREEARC_SRC:?set FREEARC_SRC to a git checkout of the C unarc sources}
image=go-unarc-freearc067

docker build -t "$image" "$here"

cd "$root"
rm -rf "$work/in" "$work/out"
mkdir -p "$work/out"
go run ./scripts/genuine inputs -src "$src" -out "$work/in"

for set in main:1 mt:4; do
	docker run --rm --network none -e GENUINE_CPUS="${set#*:}" -v "$here:/job:ro" -v "$work/in:/in:ro" -v "$work/out:/out" \
		"$image" sh /job/archives.sh "${set%:*}"
done

{
	cat "$work/out/freearc-main.txt"
	grep '^- Guest' "$work/out/freearc-mt.txt"
	cat <<'EOF'
- Download: FreeArc 0.67 of 2014-03-16, the newest build there is, published as "0.67 alpha"
  (banner `FreeArc 0.67 (March 15 2014)`). freearc.org no longer serves it; the Wayback Machine has
  the console installer
  <http://freearc.org/download/testing/0.67--2014-03-16/FreeArc-console-0.67-alpha-win32.exe>
  (SHA-256 `a229c62e2a38c4caa81aa0f2b34e398f1ae6c7b62e719f685127b82efdb861c1`, snapshot 20160711163446)
  and the portable zip
  <http://freearc.org/download/testing/0.67--2014-03-16/FreeArc-portable-0.67-alpha-win32.zip>
  (SHA-256 `fc3d5d47b602441a16e56e363f954d08915baa9d257a814b305089a181c0be5b`, snapshot 20151211000136).
  `freearc.org/download/0.67/` has no archived binaries, and there is no Linux build of 0.67 (the
  last one is 0.666). Arc.exe is the same file in both downloads; the Dockerfile takes it, with the
  rest of the console installer's `bin` folder (stock `arc.ini` and `arc.groups` included), from the
  zip.
- How it runs: Debian 12 i386 with Wine 8 (`wine32`), booted by `qemu-system-i386` (TCG, one host
  thread for all vCPUs) inside the Docker image `go-unarc-freearc067`. User-mode emulation does not
  work: under `qemu-i386` Arc.exe spins forever in `a` and `t` (one thread busy-waits, the others
  wait on events; under Rosetta with macOS Wine it hangs too), and FreeArc 0.666's Linux i386 build
  dies with GHC's "internal error: stg_ap_p_ret" or hangs.
- CPU count matters: with more than one CPU, FreeArc 0.67 builds the presets `-m1`..`-m5` and `-m2x`
  from multi-threaded `4x4` methods (and `-m3`/`-m4` text from grzip instead of dict+lzp). The
  "main" set ran on 1 CPU, the `genuine_*_mt4` and `genuine_4x4_*` archives on 4.
EOF
} >"$work/info.md"

go run ./scripts/genuine golden -from "$work/out" -unarc "$unarc" -dest testdata/genuine -info "$work/info.md"
echo "work folder: $work"
