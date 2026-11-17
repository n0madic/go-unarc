#!/bin/sh
# Creates the genuine FreeArc 0.67 test archives. Runs inside the emulated i386 guest (vm.sh, guest-init.sh)
# with the inputs at /in (go run ./scripts/genuine inputs):
#
#   sh archives.sh main   on a 1-CPU guest: everything but the multi-threaded presets
#   sh archives.sh mt     on a 4-CPU guest: the presets that FreeArc builds from "4x4" there
#
# and adds to:
#
#   /out/archives/<name>.arc   the archives
#   /out/commands.tsv          name, password, note, command line (for "go run ./scripts/genuine golden")
#   /out/logs/<name>.log       FreeArc's output when creating and then testing the archive
#   /out/freearc-<set>.txt     facts about the guest for the README
#
# Every archive is tested with FreeArc itself ("arc t") right after it is made; the job fails if FreeArc
# fails to create or to test any of them.
set -u

PASSWORD=test-password
FREEARC=/opt/freearc
OUT=/out
failed=""

# q prints its argument quoted for a POSIX shell when it needs quotes.
q() {
	case $1 in
	*[!A-Za-z0-9_./:=+%,@-]* | "") printf "'%s'" "$(printf %s "$1" | sed "s/'/'\\\\''/g")" ;;
	*) printf %s "$1" ;;
	esac
}

# arc runs FreeArc 0.67 (the console Arc.exe) under Wine.
arc() { wine "$FREEARC/Arc.exe" "$@"; }

# mk NAME NOTE ARGS... runs "arc ARGS" in /in with "@" in ARGS replaced by the archive path, then tests the
# archive. The password is passed to the test when ARGS have -p or -hp with it. Archive names get the
# prefix "genuine_": they stay apart from testdata/archives (same golden layout, often the same method).
mk() {
	name=genuine_$1 note=$2
	shift 2
	file=$OUT/archives/$name.arc
	pw="" shown="arc"
	for a do
		shift
		case $a in
		@) a=$file ;;
		-p"$PASSWORD" | -hp"$PASSWORD") pw=$PASSWORD ;;
		esac
		set -- "$@" "$a"
		shown="$shown $(q "$a")"
	done
	log=$OUT/logs/$name.log
	echo "== $name: $shown"
	rm -f "$file"
	(cd /in && arc "$@") >"$log" 2>&1
	code=$?
	# With -sfx FreeArc writes <name>.exe whatever the archive name says.
	if [ ! -f "$file" ] && [ -f "${file%.arc}.exe" ]; then
		mv "${file%.arc}.exe" "$file"
	fi
	if [ $code -ne 0 ] || [ ! -f "$file" ]; then
		echo "   FAILED to create (exit $code), see logs/$name.log"
		tail -n 5 "$log"
		failed="$failed $name"
		return
	fi
	if [ -n "$pw" ]; then
		arc t -p"$pw" "$file" >>"$log" 2>&1
	else
		arc t "$file" >>"$log" 2>&1
	fi
	code=$?
	if [ $code -ne 0 ]; then
		echo "   FreeArc's own test FAILED (exit $code), see logs/$name.log"
		failed="$failed $name"
		return
	fi
	printf '%s\t%s\t%s\t%s\n' "$name" "$pw" "$note" "$shown" >>"$OUT/commands.tsv"
	echo "   $(wc -c <"$file") bytes"
}

# The archives of the 4-CPU guest. With more than one CPU, FreeArc 0.67 takes the multi-threaded
# alternatives of its presets (the right side of "a | b" in --print-config): 4x4:tor, 4x4:lzma, 4x4:ppmd and
# grzip instead of dict+lzp+ppmd for text. -m6 and up are the same on any CPU count.
mt_set() {
	for level in 1 2 3 4 5; do
		mk m${level}_mt4 "Preset \`-m$level\` on 4 CPUs (multi-threaded 4x4 methods)." a -m$level @ mix
	done
	mk m2x_mt4 "Preset \`-m2x\` on 4 CPUs." a -m2x @ mix
	mk 4x4_lzma "Explicit \`4x4:lzma\` (LZMA split into blocks compressed in parallel)." a -m4x4:lzma:1mb @ text/1mb.txt
	mk 4x4_ppmd "Explicit 4x4 with a block size: \`4x4:b256kb:ppmd:8:16mb\`." a -m4x4:b256kb:ppmd:8:16mb @ text/1mb.txt
}

# The archives of the 1-CPU guest.
main_set() {
	# --- Presets: the mix of text, binary, x86 code, audio, empty files and folders.
	mk m0 "Preset \`-m0\` (storing), on small files." a -m0 @ misc
	for level in 1 2 3 4 5 6 7 8 9; do
		mk m$level "Preset \`-m$level\`." a -m$level @ mix
	done
	mk mx "Preset \`-mx\` (maximum internal compression)." a -mx @ mix
	mk m2x "Preset \`-m2x\` (fast decompression variant)." a -m2x @ mix
	mk m9x "Preset \`-m9x\`." a -m9x @ mix

	# --- LZMA
	mk lzma_64kb "LZMA with a 64 KiB dictionary on 256 KiB of text (larger than the dictionary)." \
		a -mlzma:64kb @ text/small.txt
	mk lzma_256kb "LZMA with a 256 KiB dictionary on 1 MiB of text, table and x86 data." \
		a -mlzma:256kb @ text/small.txt bin/table.bin bin/x86.bin
	mk lzma_fast "LZMA fast mode (hc4 match finder)." a -mlzma:fast:8mb @ text/small.txt bin/table.bin
	mk lzma_fb128 "LZMA with fb128 and a long match chain." a -mlzma:8mb:fb128:mc256 @ text/small.txt bin/x86.bin
	mk lzma_lc0lp2pb0 "LZMA with lc0, lp2, pb0." a -mlzma:1mb:lc0:lp2:pb0 @ bin/table.bin

	# --- PPMd. The model memory fills on the 1 MiB and 1.6 MiB texts (r0 restart, r1 cut off, r2 freeze);
	# the 32-bit encoder lays the model out differently from a 64-bit build.
	mk ppmd_o2 "PPMd order 2." a -mppmd:2:1mb @ text/small.txt
	mk ppmd_o4 "PPMd order 4, 16 MiB." a -mppmd:4:16mb @ text/small.txt
	mk ppmd_o8 "PPMd order 8, 96 MiB." a -mppmd:8:96mb @ text/small.txt
	mk ppmd_o16 "PPMd order 16, 192 MiB." a -mppmd:16:192mb @ text/small.txt
	mk ppmd_fill_r0 "PPMd order 8 in 1 MiB on 1 MiB of text: fills the memory, r0." a -mppmd:8:1mb:r0 @ text/1mb.txt
	mk ppmd_fill_r1 "PPMd order 8 in 1 MiB on 1 MiB of text: fills the memory, r1." a -mppmd:8:1mb:r1 @ text/1mb.txt
	mk ppmd_fill_r2 "PPMd order 8 in 1 MiB on 1 MiB of text: fills the memory, r2." a -mppmd:8:1mb:r2 @ text/1mb.txt
	mk ppmd_fill_o16 "PPMd order 16 in 2 MiB on 1 MiB of text: fills the memory (default restoration)." \
		a -mppmd:16:2mb @ text/1mb.txt
	mk ppmd_fill_o4 "PPMd order 4 in 256 KiB on 1 MiB of text: fills the memory, r1." a -mppmd:4:256kb:r1 @ text/1mb.txt
	mk ppmd_fill_big "PPMd order 12 in 4 MiB on all sources (1.6 MiB): fills the memory, r1." \
		a -mppmd:12:4mb:r1 @ text/all.txt

	# --- Tornado presets.
	for level in 1 3 5 7 9 11; do
		mk tor_$level "Tornado preset \`tor:$level\`." a -mtor:$level @ text/small.txt bin/x86.bin
	done

	# --- GRZip modes.
	for mode in 1 2 3 4; do
		mk grzip_m$mode "GRZip mode m$mode." a -mgrzip:m$mode @ text/small.txt
	done
	mk grzip_nolzp "GRZip m1 without its LZP stage." a -mgrzip:m1:l @ text/small.txt
	mk grzip_blocks "GRZip m2 with 64 KiB blocks." a -mgrzip:m2:b64kb @ text/small.txt

	# --- Preprocessors.
	mk rep_lzma "REP (1 MiB) before LZMA (64 KiB): repeats farther than LZMA's dictionary." \
		a -mrep:1mb+lzma:64kb @ bin/far.bin
	mk delta_lzma "Delta before LZMA on fixed-size records." a -mdelta+lzma:1mb @ bin/table.bin
	mk delta_tor "Delta before Tornado." a -mdelta+tor:3 @ bin/table.bin
	mk exe_lzma "x86 filter before LZMA." a -mexe+lzma:1mb @ bin/x86.bin
	# dispack070 ("exe2"), the x86 filter repacks run before delta and LZMA: on real code (FreeArc's own
	# unarc.exe and arc-tiny.sfx, compiled C: code and stored records), and on text and the synthetic x86
	# data (the text stored as whole chunks, the x86 data filtered as one code record).
	mk dispack_exe "dispack070 on x86 programs: FreeArc's unarc.exe and arc-tiny.sfx." \
		a -mdispack070+lzma:8mb -dp"$FREEARC" @ unarc.exe arc-tiny.sfx
	mk dispack_mixed "dispack070 on text and synthetic x86 data: the text is stored as whole 16 KiB chunks, the x86 data becomes one code record." \
		a -mdispack070+lzma:8mb @ bin/x86.bin text/small.txt
	mk dict_lzp_ppmd "Dict, LZP and PPMd with default parameters." a -mdict+lzp+ppmd @ text/small.txt
	mk dict_lzp_ppmd_tuned "Dict, LZP and PPMd as arc.ini's 4tn." \
		a -mdict:p:96m:80%+lzp:64m:65:d1m:s16:h20:90%+ppmd:8:192m @ text/small.txt
	mk lzp_ppmd "LZP and PPMd." a -mlzp:64:h18+ppmd:10:48mb @ text/small.txt

	# --- Audio.
	for w in u8_mono s16_mono s16_stereo s24_stereo; do
		mk tta_$w "TTA on wav/$w.wav." a -mtta @ wav/$w.wav
	done
	mk mm_lzma "mm before LZMA on 16-bit stereo." a -mmm+lzma:1mb @ wav/s16_stereo.wav
	mk mm_u8 "mm before LZMA on 8-bit mono." a -mmm+lzma:1mb @ wav/u8_mono.wav
	mk mm_tor "mm before Tornado on 24-bit stereo." a -mmm+tor:3 @ wav/s24_stereo.wav
	mk wav_presets "All WAVs with \`-m4\`: FreeArc picks the audio method per file." a -m4 -s- @ wav

	# --- Encryption (password test-password).
	crypt() { mk "$1" "$2" a -mlzma:1mb -p$PASSWORD "$3" @ crypt; }
	mk aes "Default encryption (\`-p\` alone)." a -mlzma:1mb -p$PASSWORD @ crypt
	crypt aes_cfb "AES-256 in CFB mode." -ae=aes/cfb
	crypt aes128 "AES-128 in CTR mode." -ae=aes-128
	crypt aes192_cfb "AES-192 in CFB mode." -ae=aes-192/cfb
	crypt aes_r20_n2000 "AES-128 with \`r20\` (AES-128 has 10 rounds) and 2000 PBKDF2 iterations. LibTomCrypt rejects the round count, FreeArc and the C unarc ignore the error: the data is stored unencrypted." -ae=aes-128:r20:n2000
	crypt blowfish "Blowfish (448-bit key) in CTR mode." -ae=blowfish
	crypt blowfish_cfb "Blowfish in CFB mode." -ae=blowfish/cfb
	crypt blowfish64 "Blowfish with a 64-bit key." -ae=blowfish-64
	crypt twofish "Twofish in CTR mode." -ae=twofish
	crypt twofish_cfb "Twofish in CFB mode." -ae=twofish/cfb
	crypt serpent "Serpent in CTR mode." -ae=serpent
	crypt serpent_cfb "Serpent in CFB mode." -ae=serpent/cfb
	crypt cascade "Three ciphers in a row (the documentation's example)." -ae=aes-128+serpent:n5000+twofish/cfb
	mk headers_aes "Encrypted headers (\`-hp\`) with AES." a -mlzma:1mb -hp$PASSWORD @ crypt
	mk headers_serpent "Encrypted headers with Serpent in CFB mode." a -mlzma:1mb -hp$PASSWORD -ae=serpent/cfb @ crypt

	# --- Archive structure.
	mk comment "Archive comment from a file (\`-z\`)." a -m2 -zmisc/comment.txt @ misc/hello.txt misc/sub
	mk solid3 "Solid blocks of 3 files (\`-s3\`)." a -m2 -s3 @ misc/sub
	mk nonsolid "One solid block per file (\`-s-\`)." a -m2 -s- @ misc/sub
	mk dirblocks "Directory blocks of 4 files, solid blocks of 2 (\`-s4;2\`)." a -m2 '-s4;2' @ misc
	mk sort_name "Files sorted by name (\`-dsn\`)." a -m2 -dsn @ misc
	mk nosort "No sorting (\`-ds\`)." a -m2 -ds @ misc
	mk dirs "Folders, an empty folder and an empty file, added with \`--dirs\`." a -m2 --dirs @ misc
	mk tree "A source tree." a -m3 @ src/Compression/LZMA
	mk empty_file "Only an empty file." a -m2 @ misc/empty.txt
	mk nodates "No file times (\`--nodates\`)." a -m2 --nodates @ misc/sub
	mk dirmethod_storing "Directory not compressed (\`-dm0\`)." a -m2 -dm0 @ misc/sub
	mk dirmethod_ppmd "Directory compressed with PPMd (\`-dm=ppmd:4:1mb\`)." a -m2 -dm=ppmd:4:1mb @ misc/sub
	mk recovery "With a recovery record (\`-rr4%\`)." a -m2 -rr4% @ misc
	mk sfx "Self-extracting with the smallest module (\`-sfx=arc-tiny.sfx\`); FreeArc names it sfx.exe." \
		a -m2 -sfx=arc-tiny.sfx @ misc/sub
}

set=${1:-main}
mkdir -p "$OUT/archives" "$OUT/logs"
# One wineserver for all the runs. Start Wine's background services with their output going nowhere: they
# outlive the command that starts them and would keep its output pipe open.
wineserver -p
wine wineboot >/dev/null 2>&1
arc >/tmp/banner.txt 2>&1
{
	echo "- FreeArc banner: \`$(head -n 1 /tmp/banner.txt | tr -d '\r')\`"
	echo "- Arc.exe SHA-256: \`$(sha256sum "$FREEARC/Arc.exe" | cut -d' ' -f1)\`"
	echo "- Guest for the \"$set\" set: Wine \`$(wine --version)\`, kernel \`$(uname -r)\` ($(uname -m)), $(($(sed -n 's/^MemTotal: *\([0-9]*\) kB/\1/p' /proc/meminfo) / 1024)) MiB RAM, $(nproc) CPU"
} >"$OUT/freearc-$set.txt"
case $set in
main) main_set ;;
mt) mt_set ;;
*)
	echo "unknown set $set"
	exit 2
	;;
esac
wineserver -k
if [ -n "$failed" ]; then
	echo "FAILED:$failed"
	exit 1
fi
echo "all archives made and tested"
