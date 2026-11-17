#!/usr/bin/env bash
# Builds fagen (test-data generator) from FreeArc's C++ compression library, this time with the compressors
# (no FREEARC_DECOMPRESS_ONLY). Output: .build/fagen
#
#   FREEARC_SRC=/path/to/unarc scripts/fagen/build.sh
#
# FREEARC_SRC is the folder of the C unarc sources that holds Environment.cpp and Compression/ (see the README).
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
[ -f "${FREEARC_SRC:-}/Environment.cpp" ] || {
  echo "fagen: set FREEARC_SRC to the C unarc sources (the folder with Environment.cpp and Compression/)" >&2
  exit 2
}
src=$(cd "$FREEARC_SRC" && pwd)
out="$root/.build"
obj="$out/fagen-obj"
mkdir -p "$obj"

CXX=${CXX:-clang++}
flags=(-O2 -w -fno-exceptions -fno-rtti -fno-strict-aliasing -std=gnu++98
       -DFREEARC_UNIX -DFREEARC_INTEL_BYTE_ORDER -DFREEARC_64BIT -D_NO_EXCEPTIONS -DUNARC
       -I"$src/shim")

compile() {  # compile <source> [extra flags...]
  local s=$1; shift
  local o="$obj/$(basename "${s%.*}").o"
  if [ ! -f "$o" ] || [ "$s" -nt "$o" ]; then
    "$CXX" -c "${flags[@]}" "$@" -o "$o" "$s"
  fi
  echo "$o"
}

objs=()
objs+=("$(compile "$src/Environment.cpp")")
objs+=("$(compile "$src/Compression/Common.cpp")")
objs+=("$(compile "$src/Compression/CompressionLibrary.cpp")")
# LZMA's multithreaded match finder (COMPRESS_MF_MT) stores pointers in UInt32 and does not compile for 64-bit;
# fagen runs single-threaded anyway, so build a copy of C_LZMA.cpp without it. MatchFinder_Construct leaves
# p->son uninitialised and MatchFinder_Create frees it, so the copy also zeroes what the encoder allocates.
LC_ALL=C sed -e 's|^#define COMPRESS_MF_MT|// COMPRESS_MF_MT disabled by fagen|' \
             -e 's|return BigAlloc(size); }|return calloc(1, size); }|' "$src/Compression/LZMA2/C_LZMA.cpp" > "$obj/C_LZMA_st.cpp"
objs+=("$(compile "$obj/C_LZMA_st.cpp" -I"$src/Compression/LZMA2" -I"$src/Compression")")
for c in LZMA2/C_BCJ Dict/C_Dict Delta/C_Delta REP/C_REP LZP/C_LZP MM/C_MM MM/C_TTA \
         Tornado/C_Tornado GRZip/C_GRZip; do
  objs+=("$(compile "$src/Compression/$c.cpp")")
done
objs+=("$(compile "$src/Compression/PPMD/C_PPMD.cpp" -O1)")
objs+=("$(compile "$src/Compression/_Encryption/C_Encryption.cpp" -I"$src/Compression" \
          -I"$src/Compression/_Encryption/headers" -I"$src/Compression/_Encryption")")
objs+=("$(compile "$here/fagen.cpp" -I"$src")")

"$CXX" "${objs[@]}" -lc++ -lpthread -o "$out/fagen"
echo "$out/fagen"
