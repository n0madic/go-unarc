#!/usr/bin/env bash
# Differential check of the Go unarc against the C one: for each archive, runs l, v, t and x with both
# and compares stdout, stderr, the exit code and (for x) the extracted tree.
#   scripts/diffcheck.sh                 every archive in testdata/archives and testdata/genuine/archives
#   scripts/diffcheck.sh a.arc data1.bin  the archives given
# C_UNARC names the C unarc binary to compare with (required: the C unarc is not part of this repository, see
# the README); UNARC_PASSWORD and the other UNARC_* variables pass through to both. Exits 1 if anything
# differs.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
c_unarc=${C_UNARC:-}
go_unarc=$root/.build/go-unarc
timeout_s=${DIFFCHECK_TIMEOUT:-600}

[ -n "$c_unarc" ] || { echo "diffcheck: set C_UNARC to the C unarc binary (see the README)" >&2; exit 2; }
[ -x "$c_unarc" ] || { echo "diffcheck: C_UNARC=$c_unarc is not an executable file" >&2; exit 2; }
c_unarc=$(cd "$(dirname "$c_unarc")" && pwd)/$(basename "$c_unarc")
timeout_cmd=$(command -v timeout || command -v gtimeout) || {
  echo "diffcheck: install timeout (GNU coreutils)" >&2
  exit 2
}
(cd "$root" && go build -o "$go_unarc" ./cmd/unarc)

# Both programs get the same helper tools, so that the "[cls] <tool> ..." lines name the same paths.
c_dir=$(dirname "$c_unarc")
if [ -z "${UNARC_SREP:-}" ] && [ -x "$c_dir/srep" ]; then export UNARC_SREP="$c_dir/srep"; fi
if [ -z "${UNARC_CLSHOST:-}" ] && [ -f "$c_dir/clshost.exe" ]; then export UNARC_CLSHOST="$c_dir/clshost.exe"; fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
export TZ=UTC

archives=("$@")
if [ ${#archives[@]} -eq 0 ]; then
  shopt -s nullglob
  archives=("$root"/testdata/archives/*.arc "$root"/testdata/genuine/archives/*.arc)
fi

# run <binary> <name> <cmd> <archive>: runs in an empty folder, with no input (C loops on EOF at a prompt).
run() {
  local bin=$1 name=$2 cmd=$3 archive=$4 code=0
  rm -rf "$work/$name" && mkdir -p "$work/$name"
  (cd "$work/$name" || exit 125
   exec "$timeout_cmd" "$timeout_s" "$bin" "$cmd" "$archive" </dev/null >"$work/$name.out" 2>"$work/$name.err") || code=$?
  case $code in
    0|1|2) echo "$code" >"$work/$name.code" ;;
    *) echo "diffcheck: $name could not complete $cmd on $(basename "$archive") (status $code)" >&2
       cat "$work/$name.err" >&2
       return 2 ;;
  esac
}

# Intentional differences, not run (archive:command patterns):
# - the C unarc extracts unsafe paths, writing outside its folder (even to /tmp), where the Go one stops with
#   "unsafe path in archive";
# - PPMd that filled its model memory: the 64-bit C unarc fails the CRC check, the Go one decodes it;
# - 4x4 and dispack070: the C unarc does not have the methods, the Go one decodes them
#   (all: internal/cli TestGenuineNoGolden).
skip=("unsafe_paths.arc:x" "genuine_ppmd_fill_*.arc:[tx]" "genuine_*_mt4.arc:[tx]" "genuine_4x4_*.arc:[tx]"
	"genuine_dispack_*.arc:[tx]")

# normalize drops what differs from run to run: the progress of srep ("11%: 1 -> 2: 50%.  Cpu ... Remains 00:02",
# or 01:02:03 past an hour, erased with \r and \b).
normalize() {
  perl -0pe 's/\d+%: [^\r\n]*?Remains \d\d(?::\d\d)+[ \t]*//g; s/[\r\x08]+//g' "$1" >"$1.norm"
}

# The Go unarc prints its own header (the first line of its help) where the C one prints "UnArc 0.60RC unpacker";
# normalize_out puts the Go header in the C output.
GO_HEADER=$("$go_unarc" 2>/dev/null | head -n 1 || true)
export GO_HEADER
normalize_out() {
  perl -0pe 's/\AUnArc 0\.60RC unpacker/$ENV{GO_HEADER}/' "$1" >"$1.norm"
}

failed=0 checked=0
for a in "${archives[@]}"; do
  abs=$(cd "$(dirname "$a")" && pwd)/$(basename "$a")
  for cmd in l v t x; do
    skipped=
    for pattern in "${skip[@]}"; do
      # shellcheck disable=SC2053 # the pattern is meant to glob
      [[ "$(basename "$a"):$cmd" == $pattern ]] && skipped=1
    done
    if [ -n "$skipped" ]; then
      echo "skip $(basename "$a") $cmd: intentional difference"
      continue
    fi
    run "$c_unarc" c "$cmd" "$abs"
    run "$go_unarc" go "$cmd" "$abs"
    checked=$((checked + 1))
    diffs=()
    normalize "$work/c.err"
    normalize "$work/go.err"
    normalize_out "$work/c.out"
    normalize_out "$work/go.out"
    for part in out.norm err.norm code; do
      cmp -s "$work/c.$part" "$work/go.$part" || diffs+=("${part%.norm}")
    done
    if [ "$cmd" = x ] && ! diff -r "$work/c" "$work/go" >/dev/null; then
      diffs+=(files)
    fi
    if [ ${#diffs[@]} -gt 0 ]; then
      failed=$((failed + 1))
      echo "DIFF $(basename "$a") $cmd: ${diffs[*]}"
      for part in out.norm err.norm code; do
        if ! cmp -s "$work/c.$part" "$work/go.$part"; then
          diff -u --label "C $part" --label "Go $part" "$work/c.$part" "$work/go.$part" | head -20 || true
        fi
      done
    fi
  done
done
echo "diffcheck: $checked runs, $failed differ"
[ "$checked" -gt 0 ] || { echo "diffcheck: no cases were compared" >&2; exit 2; }
[ "$failed" -eq 0 ]
