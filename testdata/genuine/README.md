# Archives made by the genuine FreeArc 0.67

The archives in `archives/` were created by FreeArc 0.67 itself: the official 32-bit Windows console
build, run under 32-bit Wine in an emulated i386 PC. Unlike `testdata/archives` (our own writer around
streams from FreeArc's encoders built for 64-bit macOS), they have FreeArc's real headers and method
strings, and their PPMd streams that fill the model memory come from the 32-bit memory layout that end
users' archives have.

`golden/<name>.<N>` is the C unarc run on `archives/<name>.arc`, with
N = 0 for `l`, 1 for `v`, 2 for `t`, in an empty scratch folder, with only `TZ=UTC`, `PATH` and,
for encrypted archives, `UNARC_PASSWORD=test-password` set. `internal/cli`'s TestGolden replays them.
Archives the C unarc fails on have no golden files; they are listed at the end.

Everything here is generated; do not edit it by hand. Rebuild with

    scripts/genuine/build.sh

which builds the Docker image `go-unarc-freearc067`, writes the inputs
(`go run ./scripts/genuine inputs`), boots the guest to run `scripts/genuine/archives.sh`, then
records the golden files and this README (`go run ./scripts/genuine golden`). FreeArc's output is
deterministic: a rebuild gives the same archives, except the encrypted ones (random salts and IVs).

## FreeArc and the build

- FreeArc banner: `FreeArc 0.67 (March 15 2014)  http://freearc.org  March 15 2014`
- Arc.exe SHA-256: `b36b1be0a3c329675af4eece3193f08cf343ede57a6933033bf6004a50ab2a65`
- Guest for the "main" set: Wine `wine-8.0 (Debian 8.0~repack-4)`, kernel `6.1.0-56-686` (i686), 3025 MiB RAM, 1 CPU
- Guest for the "mt" set: Wine `wine-8.0 (Debian 8.0~repack-4)`, kernel `6.1.0-56-686` (i686), 3025 MiB RAM, 4 CPU
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

## Archives

94 archives, 10011220 bytes. Each command ran in the input folder as `wine Arc.exe` with the arguments
shown (`arc` stands for it). Encrypted archives use the password `test-password`. "Blocks" are the
methods of the archive's data blocks as the C unarc reads them (`UNARC_BLOCKS=1`); FreeArc gives
empty files a block of their own.

| Archive | What | Command | Size | Blocks |
|:--|:--|:--|--:|:--|
| genuine_m0 | Preset `-m0` (storing), on small files. | `arc a -m0 /out/archives/genuine_m0.arc misc` | 30173 | `storing`<br>`storing` |
| genuine_m1 | Preset `-m1`. | `arc a -m1 /out/archives/genuine_m1.arc mix` | 262319 | `storing`<br>`rep:606kb:256+tor:4:606kb` |
| genuine_m2 | Preset `-m2`. | `arc a -m2 /out/archives/genuine_m2.arc mix` | 210285 | `storing`<br>`grzip:276kb:m4:l32:h15`<br>`rep:292kb:256+exe+tor:6:292kb:h2mb`<br>`rep:9kb:256+tor:4:9kb:h64kb`<br>`tta:m1` |
| genuine_m3 | Preset `-m3`. | `arc a -m3 /out/archives/genuine_m3.arc mix` | 147287 | `storing`<br>`dict:276kb:85%:l8192:m400:s100+lzp:276kb:92%:24:h19+grzip:276kb:m3:l`<br>`rep:292kb+exe+delta+lzma:292kb:fast:32:mc4`<br>`rep:9kb+tor:4:9kb:h64kb`<br>`tta` |
| genuine_m4 | Preset `-m4`. | `arc a -m4 /out/archives/genuine_m4.arc mix` | 139226 | `storing`<br>`dict:276kb:80%:l8192:m400:s100+lzp:276kb:90%:65:h19:d1mb:s16+ppmd:8:96mb`<br>`rep:292kb+exe+delta+lzma:292kb:normal:16:mc8`<br>`rep:9kb+tor:9kb:h64kb:c3`<br>`tta` |
| genuine_m5 | Preset `-m5`. | `arc a -m5 /out/archives/genuine_m5.arc mix` | 137451 | `storing`<br>`dict:276kb:80%:l8192:m400:s100+lzp:276kb:92%:105:h19:d1mb+ppmd:12:192mb`<br>`rep:300kb+exe+delta+lzma:300kb:normal:32:mc32`<br>`tta` |
| genuine_m6 | Preset `-m6`. | `arc a -m6 /out/archives/genuine_m6.arc mix` | 134817 | `storing`<br>`dict:276kb:80%:l8192:m400:s100+lzp:276kb:92%:145:h19:d1mb+ppmd:16:384mb`<br>`rep:300kb+exe+delta+lzma:300kb:normal:bt4:128`<br>`tta` |
| genuine_m7 | Preset `-m7`. | `arc a -m7 /out/archives/genuine_m7.arc mix` | 134817 | `storing`<br>`dict:276kb:80%:l8192:m400:s100+lzp:276kb:92%:145:h19:d1mb+ppmd:16:384mb`<br>`rep:300kb+exe+delta+lzma:300kb:normal:bt4:128`<br>`tta` |
| genuine_m8 | Preset `-m8`. | `arc a -m8 /out/archives/genuine_m8.arc mix` | 134817 | `storing`<br>`dict:276kb:80%:l8192:m400:s100+lzp:276kb:92%:145:h19:d1mb+ppmd:16:384mb`<br>`rep:300kb+exe+delta+lzma:300kb:normal:bt4:128`<br>`tta` |
| genuine_m9 | Preset `-m9`. | `arc a -m9 /out/archives/genuine_m9.arc mix` | 134817 | `storing`<br>`dict:276kb:80%:l8192:m400:s100+lzp:276kb:92%:145:h19:d1mb+ppmd:16:384mb`<br>`rep:300kb+exe+delta+lzma:300kb:normal:bt4:128`<br>`tta` |
| genuine_mx | Preset `-mx` (maximum internal compression). | `arc a -mx /out/archives/genuine_mx.arc mix` | 134817 | `storing`<br>`dict:276kb:80%:l8192:m400:s100+lzp:276kb:92%:145:h19:d1mb+ppmd:16:384mb`<br>`rep:300kb+exe+delta+lzma:300kb:normal:bt4:128`<br>`tta` |
| genuine_m2x | Preset `-m2x` (fast decompression variant). | `arc a -m2x /out/archives/genuine_m2x.arc mix` | 218701 | `storing`<br>`exe+tor:6:292kb:h2mb`<br>`rep:9kb:256+tor:4:9kb:h64kb`<br>`tor:6:276kb:h2mb`<br>`tta:m1` |
| genuine_m9x | Preset `-m9x`. | `arc a -m9x /out/archives/genuine_m9x.arc mix` | 138273 | `storing`<br>`dict:276kb:75%+lzma:276kb:normal:bt4:128`<br>`exe+delta+lzma:300kb:normal:bt4:128`<br>`tta:m1` |
| genuine_lzma_64kb | LZMA with a 64 KiB dictionary on 256 KiB of text (larger than the dictionary). | `arc a -mlzma:64kb /out/archives/genuine_lzma_64kb.arc text/small.txt` | 64504 | `lzma:64kb:normal:32` |
| genuine_lzma_256kb | LZMA with a 256 KiB dictionary on 1 MiB of text, table and x86 data. | `arc a -mlzma:256kb /out/archives/genuine_lzma_256kb.arc text/small.txt bin/table.bin bin/x86.bin` | 377498 | `lzma:256kb:normal:32` |
| genuine_lzma_fast | LZMA fast mode (hc4 match finder). | `arc a -mlzma:fast:8mb /out/archives/genuine_lzma_fast.arc text/small.txt bin/table.bin` | 237263 | `lzma:777kb:fast:32` |
| genuine_lzma_fb128 | LZMA with fb128 and a long match chain. | `arc a -mlzma:8mb:fb128:mc256 /out/archives/genuine_lzma_fb128.arc text/small.txt bin/x86.bin` | 221192 | `lzma:518kb:normal:128:mc256` |
| genuine_lzma_lc0lp2pb0 | LZMA with lc0, lp2, pb0. | `arc a -mlzma:1mb:lc0:lp2:pb0 /out/archives/genuine_lzma_lc0lp2pb0.arc bin/table.bin` | 168267 | `lzma:518kb:normal:32:pb0:lc0:lp2` |
| genuine_ppmd_o2 | PPMd order 2. | `arc a -mppmd:2:1mb /out/archives/genuine_ppmd_o2.arc text/small.txt` | 77247 | `ppmd:2:1mb` |
| genuine_ppmd_o4 | PPMd order 4, 16 MiB. | `arc a -mppmd:4:16mb /out/archives/genuine_ppmd_o4.arc text/small.txt` | 56714 | `ppmd:4:16mb` |
| genuine_ppmd_o8 | PPMd order 8, 96 MiB. | `arc a -mppmd:8:96mb /out/archives/genuine_ppmd_o8.arc text/small.txt` | 52240 | `ppmd:8:96mb` |
| genuine_ppmd_o16 | PPMd order 16, 192 MiB. | `arc a -mppmd:16:192mb /out/archives/genuine_ppmd_o16.arc text/small.txt` | 50847 | `ppmd:16:192mb` |
| genuine_ppmd_fill_r0 (no golden) | PPMd order 8 in 1 MiB on 1 MiB of text: fills the memory, r0. | `arc a -mppmd:8:1mb:r0 /out/archives/genuine_ppmd_fill_r0.arc text/1mb.txt` | 201998 | `ppmd:8:1mb` |
| genuine_ppmd_fill_r1 (no golden) | PPMd order 8 in 1 MiB on 1 MiB of text: fills the memory, r1. | `arc a -mppmd:8:1mb:r1 /out/archives/genuine_ppmd_fill_r1.arc text/1mb.txt` | 190863 | `ppmd:8:1mb:r` |
| genuine_ppmd_fill_r2 (no golden) | PPMd order 8 in 1 MiB on 1 MiB of text: fills the memory, r2. | `arc a -mppmd:8:1mb:r2 /out/archives/genuine_ppmd_fill_r2.arc text/1mb.txt` | 275563 | `ppmd:8:1mb:r2` |
| genuine_ppmd_fill_o16 (no golden) | PPMd order 16 in 2 MiB on 1 MiB of text: fills the memory (default restoration). | `arc a -mppmd:16:2mb /out/archives/genuine_ppmd_fill_o16.arc text/1mb.txt` | 201675 | `ppmd:16:2mb` |
| genuine_ppmd_fill_o4 (no golden) | PPMd order 4 in 256 KiB on 1 MiB of text: fills the memory, r1. | `arc a -mppmd:4:256kb:r1 /out/archives/genuine_ppmd_fill_o4.arc text/1mb.txt` | 210591 | `ppmd:4:256kb:r` |
| genuine_ppmd_fill_big (no golden) | PPMd order 12 in 4 MiB on all sources (1.6 MiB): fills the memory, r1. | `arc a -mppmd:12:4mb:r1 /out/archives/genuine_ppmd_fill_big.arc text/all.txt` | 287078 | `ppmd:12:4mb:r` |
| genuine_tor_1 | Tornado preset `tor:1`. | `arc a -mtor:1 /out/archives/genuine_tor_1.arc text/small.txt bin/x86.bin` | 404931 | `tor:1:518kb` |
| genuine_tor_3 | Tornado preset `tor:3`. | `arc a -mtor:3 /out/archives/genuine_tor_3.arc text/small.txt bin/x86.bin` | 257122 | `tor:3:518kb` |
| genuine_tor_5 | Tornado preset `tor:5`. | `arc a -mtor:5 /out/archives/genuine_tor_5.arc text/small.txt bin/x86.bin` | 244986 | `tor:518kb:h4mb` |
| genuine_tor_7 | Tornado preset `tor:7`. | `arc a -mtor:7 /out/archives/genuine_tor_7.arc text/small.txt bin/x86.bin` | 241938 | `tor:7:518kb:h4mb` |
| genuine_tor_9 | Tornado preset `tor:9`. | `arc a -mtor:9 /out/archives/genuine_tor_9.arc text/small.txt bin/x86.bin` | 241049 | `tor:9:518kb:h4mb` |
| genuine_tor_11 | Tornado preset `tor:11`. | `arc a -mtor:11 /out/archives/genuine_tor_11.arc text/small.txt bin/x86.bin` | 238243 | `tor:11:518kb:h4mb` |
| genuine_grzip_m1 | GRZip mode m1. | `arc a -mgrzip:m1 /out/archives/genuine_grzip_m1.arc text/small.txt` | 55679 | `grzip:260kb:m1:l32:h15` |
| genuine_grzip_m2 | GRZip mode m2. | `arc a -mgrzip:m2 /out/archives/genuine_grzip_m2.arc text/small.txt` | 56806 | `grzip:260kb:m2:l32:h15` |
| genuine_grzip_m3 | GRZip mode m3. | `arc a -mgrzip:m3 /out/archives/genuine_grzip_m3.arc text/small.txt` | 56332 | `grzip:260kb:m3:l32:h15` |
| genuine_grzip_m4 | GRZip mode m4. | `arc a -mgrzip:m4 /out/archives/genuine_grzip_m4.arc text/small.txt` | 57107 | `grzip:260kb:m4:l32:h15` |
| genuine_grzip_nolzp | GRZip m1 without its LZP stage. | `arc a -mgrzip:m1:l /out/archives/genuine_grzip_nolzp.arc text/small.txt` | 56439 | `grzip:260kb:m1:l` |
| genuine_grzip_blocks | GRZip m2 with 64 KiB blocks. | `arc a -mgrzip:m2:b64kb /out/archives/genuine_grzip_blocks.arc text/small.txt` | 66756 | `grzip:64kb:m2:l32:h15` |
| genuine_rep_lzma | REP (1 MiB) before LZMA (64 KiB): repeats farther than LZMA's dictionary. | `arc a -mrep:1mb+lzma:64kb /out/archives/genuine_rep_lzma.arc bin/far.bin` | 143756 | `rep:958kb+lzma:64kb:normal:32` |
| genuine_delta_lzma | Delta before LZMA on fixed-size records. | `arc a -mdelta+lzma:1mb /out/archives/genuine_delta_lzma.arc bin/table.bin` | 54366 | `delta+lzma:518kb:normal:32` |
| genuine_delta_tor | Delta before Tornado. | `arc a -mdelta+tor:3 /out/archives/genuine_delta_tor.arc bin/table.bin` | 82250 | `delta+tor:3:518kb` |
| genuine_exe_lzma | x86 filter before LZMA. | `arc a -mexe+lzma:1mb /out/archives/genuine_exe_lzma.arc bin/x86.bin` | 125867 | `exe+lzma:260kb:normal:32` |
| genuine_dispack_exe (no golden) | dispack070 on x86 programs: FreeArc's unarc.exe and arc-tiny.sfx. | `arc a -mdispack070+lzma:8mb -dp/opt/freearc /out/archives/genuine_dispack_exe.arc unarc.exe arc-tiny.sfx` | 133475 | `dispack070+lzma:459kb:normal:32` |
| genuine_dispack_mixed (no golden) | dispack070 on text and synthetic x86 data: the text is stored as whole 16 KiB chunks, the x86 data becomes one code record. | `arc a -mdispack070+lzma:8mb /out/archives/genuine_dispack_mixed.arc bin/x86.bin text/small.txt` | 207639 | `dispack070+lzma:518kb:normal:32` |
| genuine_dict_lzp_ppmd | Dict, LZP and PPMd with default parameters. | `arc a -mdict+lzp+ppmd /out/archives/genuine_dict_lzp_ppmd.arc text/small.txt` | 51055 | `dict:260kb+lzp:260kb:64:h18+ppmd:10:48mb` |
| genuine_dict_lzp_ppmd_tuned | Dict, LZP and PPMd as arc.ini's 4tn. | `arc a -mdict:p:96m:80%+lzp:64m:65:d1m:s16:h20:90%+ppmd:8:192m /out/archives/genuine_dict_lzp_ppmd_tuned.arc text/small.txt` | 51307 | `dict:260kb:80%:l8192:m400:s100+lzp:260kb:90%:65:h19:d1mb:s16+ppmd:8:192mb` |
| genuine_lzp_ppmd | LZP and PPMd. | `arc a -mlzp:64:h18+ppmd:10:48mb /out/archives/genuine_lzp_ppmd.arc text/small.txt` | 51163 | `lzp:260kb:64:h18+ppmd:10:48mb` |
| genuine_tta_u8_mono | TTA on wav/u8_mono.wav. | `arc a -mtta /out/archives/genuine_tta_u8_mono.arc wav/u8_mono.wav` | 3220 | `tta` |
| genuine_tta_s16_mono | TTA on wav/s16_mono.wav. | `arc a -mtta /out/archives/genuine_tta_s16_mono.arc wav/s16_mono.wav` | 25554 | `tta` |
| genuine_tta_s16_stereo | TTA on wav/s16_stereo.wav. | `arc a -mtta /out/archives/genuine_tta_s16_stereo.arc wav/s16_stereo.wav` | 50912 | `tta` |
| genuine_tta_s24_stereo | TTA on wav/s24_stereo.wav. | `arc a -mtta /out/archives/genuine_tta_s24_stereo.arc wav/s24_stereo.wav` | 95165 | `tta` |
| genuine_mm_lzma | mm before LZMA on 16-bit stereo. | `arc a -mmm+lzma:1mb /out/archives/genuine_mm_lzma.arc wav/s16_stereo.wav` | 66460 | `mm+lzma:88kb:normal:32` |
| genuine_mm_u8 | mm before LZMA on 8-bit mono. | `arc a -mmm+lzma:1mb /out/archives/genuine_mm_u8.arc wav/u8_mono.wav` | 3281 | `mm+lzma:32kb:normal:32` |
| genuine_mm_tor | mm before Tornado on 24-bit stereo. | `arc a -mmm+tor:3 /out/archives/genuine_mm_tor.arc wav/s24_stereo.wav` | 121922 | `mm+tor:3:132kb` |
| genuine_wav_presets | All WAVs with `-m4`: FreeArc picks the audio method per file. | `arc a -m4 -s- /out/archives/genuine_wav_presets.arc wav` | 174270 | `storing`<br>`tta`<br>`tta`<br>`tta`<br>`tta` |
| genuine_aes | Default encryption (`-p` alone). | `arc a -mlzma:1mb -ptest-password /out/archives/genuine_aes.arc crypt` | 10449 | `storing+aes-256/ctr:n1000:r0:i6a2b3a31d46f31dad8571cfbc7a6d76a:s99416a20304ff377a717ca5db9bb379e0860e9802f4421d4c044e1553e7ccb91:cf442`<br>`lzma:32kb:normal:32+aes-256/ctr:n1000:r0:ib7ea35827b798f29093793621ee86fbb:s156ccd85329beb439f5508c002b4fc8aafdfb0a208b2cd59b333779154bd8534:c6493` |
| genuine_aes_cfb | AES-256 in CFB mode. | `arc a -mlzma:1mb -ptest-password -ae=aes/cfb /out/archives/genuine_aes_cfb.arc crypt` | 10451 | `storing+aes-256/cfb:n1000:r0:i2593729791f9ecf6d55b2c79e3ea9ed1:s9ed77b5b8d59a64433f51ef7414c510adb59b3655af8f2bbc3430d282c4f78ae:cb86d`<br>`lzma:32kb:normal:32+aes-256/cfb:n1000:r0:if22846699f239ede220d9dbe0451a61f:s13ad8bb1055be5ecfa4d4d97aa848c8044dbcadb44ae09a48fca6a4514a0c562:c59d2` |
| genuine_aes128 | AES-128 in CTR mode. | `arc a -mlzma:1mb -ptest-password -ae=aes-128 /out/archives/genuine_aes128.arc crypt` | 10408 | `storing+aes-128/ctr:n1000:r0:if8d093f86c45bf6f4bbce44c33cae976:s575ec0621bd7763bb21aca6704f5d32f:caebc`<br>`lzma:32kb:normal:32+aes-128/ctr:n1000:r0:i7771097c9bd6141264fb4885a74b2d0e:sb7b3604b795923332eb99d5204c2972c:c519a` |
| genuine_aes192_cfb | AES-192 in CFB mode. | `arc a -mlzma:1mb -ptest-password -ae=aes-192/cfb /out/archives/genuine_aes192_cfb.arc crypt` | 10430 | `storing+aes-192/cfb:n1000:r0:ic9465d44cb8466f0ee394414a3b6f710:sc2e7876df5b9f3bb45dfcfa21f8146262e2e9982922ffccf:c912c`<br>`lzma:32kb:normal:32+aes-192/cfb:n1000:r0:i2f9a84315add648840842061bdca7989:se39526b54ffd8fe4a8b72719f5eedb7e6c7dddf116051e52:cdf3d` |
| genuine_aes_r20_n2000 | AES-128 with `r20` (AES-128 has 10 rounds) and 2000 PBKDF2 iterations. LibTomCrypt rejects the round count, FreeArc and the C unarc ignore the error: the data is stored unencrypted. | `arc a -mlzma:1mb -ptest-password -ae=aes-128:r20:n2000 /out/archives/genuine_aes_r20_n2000.arc crypt` | 10405 | `storing+aes-128/ctr:n2000:r20:ic25c7798d1906119929203c41a70330d:s7ce2deb8898b44109e5752b342024d4c:cb470`<br>`lzma:32kb:normal:32+aes-128/ctr:n2000:r20:i8f3b856b4008c1f37200bd4199411c86:sac5beead2a398b3677254060c83d6b63:c77f6` |
| genuine_blowfish | Blowfish (448-bit key) in CTR mode. | `arc a -mlzma:1mb -ptest-password -ae=blowfish /out/archives/genuine_blowfish.arc crypt` | 10497 | `storing+blowfish-448/ctr:n1000:r0:i8b3e53c8746be816:s1c8647d00279f402dcadf941f3e3c36f1cf0a24ed00fa61e99f022ce6519a31b90f8c367b115b91540406918dbbbf5f3a9093c9feac19f55:c4b4a`<br>`lzma:32kb:normal:32+blowfish-448/ctr:n1000:r0:ie1f8f6d1ee73c00b:sdbbdba828ee5f8b44e69fbea2843c8c778d997d7489a93d3866dc4342fab315d7f5001186da659bd5dfe8bd0c4f89b242f1bbb2f69c4aa15:c76df` |
| genuine_blowfish_cfb | Blowfish in CFB mode. | `arc a -mlzma:1mb -ptest-password -ae=blowfish/cfb /out/archives/genuine_blowfish_cfb.arc crypt` | 10495 | `storing+blowfish-448/cfb:n1000:r0:i933cf25fe0373e06:s7b3d353ab654beee64cb1c071d0425603018f52ed544c849522a28085a432a03c7a10baf0f34d9f8c14ac76182374eba2f38e365f34dae37:ca86d`<br>`lzma:32kb:normal:32+blowfish-448/cfb:n1000:r0:ib64d45757e507bc5:s133d196ab2114a1791646940b39fd258c370b763b5c4869c01eab59aa1cd414f811ad9bdffb6e30e1a9c0fde08079f6066ba3d41bc809728:c32da` |
| genuine_blowfish64 | Blowfish with a 64-bit key. | `arc a -mlzma:1mb -ptest-password -ae=blowfish-64 /out/archives/genuine_blowfish64.arc crypt` | 10360 | `storing+blowfish-64/ctr:n1000:r0:i920f6a0d7d245c45:s69cc72c20523e1b2:c7c79`<br>`lzma:32kb:normal:32+blowfish-64/ctr:n1000:r0:i52efc4572c369197:s6f1e27562f0eefa0:cb421` |
| genuine_twofish | Twofish in CTR mode. | `arc a -mlzma:1mb -ptest-password -ae=twofish /out/archives/genuine_twofish.arc crypt` | 10456 | `storing+twofish-256/ctr:n1000:r0:ibc6b25c5114104efceac8e9396e1dd2d:sba03940645a3c18932c16a0dee8167c6270784cb8fdef67206197acf810ceaa2:c2822`<br>`lzma:32kb:normal:32+twofish-256/ctr:n1000:r0:i6ad8f1ab95636bc7f59a9f981119de67:sa6aa349ab2e8c05ca1d748bff826ba1b03d13d41ff4896b33ce900467195c148:c308c` |
| genuine_twofish_cfb | Twofish in CFB mode. | `arc a -mlzma:1mb -ptest-password -ae=twofish/cfb /out/archives/genuine_twofish_cfb.arc crypt` | 10450 | `storing+twofish-256/cfb:n1000:r0:i34fd195b35da426758c28f3680deeb2f:sd8601b8b505abef5c04ef3ceb85049f4384dbe2be7086a0a1f2564fbe70f59f3:cc5a6`<br>`lzma:32kb:normal:32+twofish-256/cfb:n1000:r0:if0c432c7dc79ceb17fac176226d0e250:sb21cede39910c3f22f015f3110b79b7ec719bd345b34567db15625bf00074e50:ccd23` |
| genuine_serpent | Serpent in CTR mode. | `arc a -mlzma:1mb -ptest-password -ae=serpent /out/archives/genuine_serpent.arc crypt` | 10451 | `storing+serpent-256/ctr:n1000:r0:i8bfc894b4daa5cbecc2a042a55d3ec4e:s16675478e6306259c17a7a6897556ca1065068650a3a527211cdb4030aa94f24:c713f`<br>`lzma:32kb:normal:32+serpent-256/ctr:n1000:r0:i2f8dd1b273d51029824b631e5061f688:sfa07d12b31bcec30cce608fee28d202422a324978b2341b5dfe32b8a512295d6:c4057` |
| genuine_serpent_cfb | Serpent in CFB mode. | `arc a -mlzma:1mb -ptest-password -ae=serpent/cfb /out/archives/genuine_serpent_cfb.arc crypt` | 10456 | `storing+serpent-256/cfb:n1000:r0:i71e12e40a431dd45adfc70bb4334d7e2:s328ee9b6609424b2c293471f0c998e0b1c7c8a684cd98ec5377ecf376daabb7a:c6eaa`<br>`lzma:32kb:normal:32+serpent-256/cfb:n1000:r0:ic8f2c0c57b9f46a91b99950fc52e9f27:sf0d94cb9e405645225ff0d807eb992980a3513f33845584c7a027cc159f809bd:ca8f8` |
| genuine_cascade | Three ciphers in a row (the documentation's example). | `arc a -mlzma:1mb -ptest-password -ae=aes-128+serpent:n5000+twofish/cfb /out/archives/genuine_cascade.arc crypt` | 10695 | `storing+aes-128/ctr:n1000:r0:i25d9ab675c768a6512943bc3cb27ce8e:s5cd96d8cf233b47a1206f70692945f76:c2f4c+serpent-256/ctr:n5000:r0:ic4c577f048b5d46590cd396539b0adcf:s3719d90f86a168f5a3f2f9088fa782c929383e4153ad05fde07d7369c5c51f66:cfc4e+twofish-256/cfb:n1000:r0:icfbfd5fdeb4f25daf6090254b746d9f8:s87839163c214b585444812a0a877b6dbcab3f9c8720d8c0679cd3375bf7c66ac:cdac4`<br>`lzma:32kb:normal:32+aes-128/ctr:n1000:r0:i95815d7e3360e4023285ed92e7e00b2d:s096c595916de5ac7d6c171a06f232978:cbe64+serpent-256/ctr:n5000:r0:ied520b337303978b65e55a1ddfa71718:sf0fe3cca28ce43c65f7afdbb979691e7ddac40ba9bdde44b237f66a1ef0d3c1b:cf1c7+twofish-256/cfb:n1000:r0:i37fdf7be5a60cd9aa4294ad6bbc83280:seffb50e072e111462ac47d9c5a8238580523a42bbaff689ff7a44ba83c7a3cda:cf797` |
| genuine_headers_aes | Encrypted headers (`-hp`) with AES. | `arc a -mlzma:1mb -hptest-password /out/archives/genuine_headers_aes.arc crypt` | 10815 | `storing+aes-256/ctr:n1000:r0:ibc93dd33c6254a00049cbf8541d5cc49:s4994921d57e8202c99e264bfbf8197277fb3c82be03df02be963408e15a16efb:c5efc`<br>`lzma:32kb:normal:32+aes-256/ctr:n1000:r0:i2dfe9bd90b8473b88bab1724fe635e83:s974edaaa31741280d76a53d915cfda05f4faabb7684e229ded5a87de645b99a8:c9eb9` |
| genuine_headers_serpent | Encrypted headers with Serpent in CFB mode. | `arc a -mlzma:1mb -hptest-password -ae=serpent/cfb /out/archives/genuine_headers_serpent.arc crypt` | 10828 | `storing+serpent-256/cfb:n1000:r0:i17a08e063fd4fe74e5ae4d12ffff9741:s6abb9a0114bc42e78fdfa604fdd9b7892bbf363400a6f6f987ea8a19047aa209:c8491`<br>`lzma:32kb:normal:32+serpent-256/cfb:n1000:r0:ie16ec575ac2c19fa95bdd4e0fda961ca:s09363af2579172bb974de1e75fce68985b6bfbfa020bac488cbea8a629e19392:c8e52` |
| genuine_comment | Archive comment from a file (`-z`). | `arc a -m2 -zmisc/comment.txt /out/archives/genuine_comment.arc misc/hello.txt misc/sub` | 11400 | `storing`<br>`grzip:23kb:m4:l32:h15`<br>`rep:7kb:256+exe+tor:6:7kb:h32kb`<br>`rep:2kb:256+tor:4:2kb:h8kb` |
| genuine_solid3 | Solid blocks of 3 files (`-s3`). | `arc a -m2 -s3 /out/archives/genuine_solid3.arc misc/sub` | 11738 | `storing`<br>`grzip:11kb:m4:l32:h14`<br>`grzip:13kb:m4:l32:h14`<br>`rep:7kb:256+exe+tor:6:7kb:h32kb`<br>`rep:2kb:256+tor:4:2kb:h8kb` |
| genuine_nonsolid | One solid block per file (`-s-`). | `arc a -m2 -s- /out/archives/genuine_nonsolid.arc misc/sub` | 12447 | `storing`<br>`grzip:4kb:m4:l32:h12`<br>`grzip:6kb:m4:l32:h13`<br>`grzip:3kb:m4:l32:h12`<br>`grzip:8kb:m4:l32:h13`<br>`grzip:2kb:m4:l32:h11`<br>`grzip:5kb:m4:l32:h13`<br>`rep:7kb:256+exe+tor:6:7kb:h32kb`<br>`rep:2kb:256+tor:4:2kb:h8kb` |
| genuine_dirblocks | Directory blocks of 4 files, solid blocks of 2 (`-s4;2`). | `arc a -m2 '-s4;2' /out/archives/genuine_dirblocks.arc misc` | 12612 | `storing`<br>`rep:1kb:256+exe+tor:6:1kb:h4kb`<br>`grzip:9kb:m4:l32:h14`<br>`rep:1kb:256+exe+tor:6:1kb:h4kb`<br>`grzip:10kb:m4:l32:h14`<br>`grzip:6kb:m4:l32:h13`<br>`rep:7kb:256+exe+tor:6:7kb:h32kb`<br>`rep:2kb:256+tor:4:2kb:h8kb` |
| genuine_sort_name | Files sorted by name (`-dsn`). | `arc a -m2 -dsn /out/archives/genuine_sort_name.arc misc` | 11449 | `storing`<br>`grzip:23kb:m4:l32:h15`<br>`rep:7kb:256+exe+tor:6:7kb:h32kb`<br>`rep:2kb:256+tor:4:2kb:h8kb` |
| genuine_nosort | No sorting (`-ds`). | `arc a -m2 -ds /out/archives/genuine_nosort.arc misc` | 11440 | `storing`<br>`grzip:23kb:m4:l32:h15`<br>`rep:7kb:256+exe+tor:6:7kb:h32kb`<br>`rep:2kb:256+tor:4:2kb:h8kb` |
| genuine_dirs | Folders, an empty folder and an empty file, added with `--dirs`. | `arc a -m2 --dirs /out/archives/genuine_dirs.arc misc` | 11435 | `storing`<br>`grzip:23kb:m4:l32:h15`<br>`rep:7kb:256+exe+tor:6:7kb:h32kb`<br>`rep:2kb:256+tor:4:2kb:h8kb` |
| genuine_tree | A source tree. | `arc a -m3 /out/archives/genuine_tree.arc src/Compression/LZMA` | 39127 | `storing`<br>`dict:209kb:85%:l8192:m400:s100+lzp:209kb:92%:24:h18+grzip:209kb:m3:l` |
| genuine_empty_file | Only an empty file. | `arc a -m2 /out/archives/genuine_empty_file.arc misc/empty.txt` | 281 | `rep:1kb:256+exe+tor:6:1kb:h4kb` |
| genuine_nodates | No file times (`--nodates`). | `arc a -m2 --nodates /out/archives/genuine_nodates.arc misc/sub` | 11279 | `storing`<br>`grzip:23kb:m4:l32:h15`<br>`rep:7kb:256+exe+tor:6:7kb:h32kb`<br>`rep:2kb:256+tor:4:2kb:h8kb` |
| genuine_dirmethod_storing | Directory not compressed (`-dm0`). | `arc a -m2 -dm0 /out/archives/genuine_dirmethod_storing.arc misc/sub` | 11308 | `storing`<br>`grzip:23kb:m4:l32:h15`<br>`rep:7kb:256+exe+tor:6:7kb:h32kb`<br>`rep:2kb:256+tor:4:2kb:h8kb` |
| genuine_dirmethod_ppmd | Directory compressed with PPMd (`-dm=ppmd:4:1mb`). | `arc a -m2 -dm=ppmd:4:1mb /out/archives/genuine_dirmethod_ppmd.arc misc/sub` | 11242 | `storing`<br>`grzip:23kb:m4:l32:h15`<br>`rep:7kb:256+exe+tor:6:7kb:h32kb`<br>`rep:2kb:256+tor:4:2kb:h8kb` |
| genuine_recovery | With a recovery record (`-rr4%`). | `arc a -m2 -rr4% /out/archives/genuine_recovery.arc misc` | 12214 | `storing`<br>`grzip:23kb:m4:l32:h15`<br>`rep:7kb:256+exe+tor:6:7kb:h32kb`<br>`rep:2kb:256+tor:4:2kb:h8kb` |
| genuine_sfx | Self-extracting with the smallest module (`-sfx=arc-tiny.sfx`); FreeArc names it sfx.exe. | `arc a -m2 -sfx=arc-tiny.sfx /out/archives/genuine_sfx.arc misc/sub` | 141867 | `storing`<br>`grzip:23kb:m4:l32:h15`<br>`rep:7kb:256+exe+tor:6:7kb:h32kb`<br>`rep:2kb:256+tor:4:2kb:h8kb` |
| genuine_m1_mt4 (no golden) | Preset `-m1` on 4 CPUs (multi-threaded 4x4 methods). | `arc a -m1 /out/archives/genuine_m1_mt4.arc mix` | 261753 | `storing`<br>`rep:606kb:256+4x4:tor:3:606kb` |
| genuine_m2_mt4 (no golden) | Preset `-m2` on 4 CPUs (multi-threaded 4x4 methods). | `arc a -m2 /out/archives/genuine_m2_mt4.arc mix` | 209543 | `storing`<br>`grzip:276kb:m4:l32:h15`<br>`rep:9kb:256+4x4:tor:3:9kb:h64kb`<br>`rep:292kb:256+exe+4x4:tor:292kb:h2mb`<br>`tta:m1` |
| genuine_m3_mt4 (no golden) | Preset `-m3` on 4 CPUs (multi-threaded 4x4 methods). | `arc a -m3 /out/archives/genuine_m3_mt4.arc mix` | 146587 | `storing`<br>`grzip:276kb:m2:l32:h15`<br>`rep:9kb+4x4:tor:3:9kb:h64kb`<br>`rep:292kb+exe+delta+4x4:lzma:292kb:fast:32:mc4`<br>`tta` |
| genuine_m4_mt4 (no golden) | Preset `-m4` on 4 CPUs (multi-threaded 4x4 methods). | `arc a -m4 /out/archives/genuine_m4_mt4.arc mix` | 142174 | `storing`<br>`grzip:276kb:m1:l32:h15`<br>`rep:9kb+4x4:tor:9kb:h64kb:c3`<br>`rep:292kb+exe+delta+4x4:lzma:292kb:normal:16:mc8`<br>`tta` |
| genuine_m5_mt4 (no golden) | Preset `-m5` on 4 CPUs (multi-threaded 4x4 methods). | `arc a -m5 /out/archives/genuine_m5_mt4.arc mix` | 137972 | `storing`<br>`dict:276kb:80%:l8192:m400:s100+lzp:276kb:90%:65:h19:d1mb+4x4:b7mb:ppmd:8:96mb`<br>`rep:300kb+exe+delta+4x4:lzma:300kb:normal:32:mc32`<br>`tta` |
| genuine_m2x_mt4 (no golden) | Preset `-m2x` on 4 CPUs. | `arc a -m2x /out/archives/genuine_m2x_mt4.arc mix` | 220133 | `storing`<br>`4x4:tor:276kb:h2mb`<br>`exe+4x4:tor:292kb:h2mb`<br>`rep:9kb:256+4x4:tor:3:9kb:h64kb`<br>`tta:m1` |
| genuine_4x4_lzma (no golden) | Explicit `4x4:lzma` (LZMA split into blocks compressed in parallel). | `arc a -m4x4:lzma:1mb /out/archives/genuine_4x4_lzma.arc text/1mb.txt` | 188432 | `4x4:lzma:1mb:normal:32` |
| genuine_4x4_ppmd (no golden) | Explicit 4x4 with a block size: `4x4:b256kb:ppmd:8:16mb`. | `arc a -m4x4:b256kb:ppmd:8:16mb /out/archives/genuine_4x4_ppmd.arc text/1mb.txt` | 186751 | `4x4:b256kb:ppmd:8:16mb` |

## C unarc fails on these

### genuine_ppmd_fill_r0

PPMd filled its model memory. The 64-bit C build lays the model out differently from the 32-bit encoder, so its decoding diverges once the memory is full; the Go port is expected to decode these.

```
$ unarc t genuine/archives/genuine_ppmd_fill_r0.arc
UnArc 0.60RC unpacker. Testing archive: genuine_ppmd_fill_r0.arc
Testing text/1mb.txt (1048576 bytes)

ERROR: file text/1mb.txt failed CRC check
--- stderr

--- exit 2
```

### genuine_ppmd_fill_r1

PPMd filled its model memory. The 64-bit C build lays the model out differently from the 32-bit encoder, so its decoding diverges once the memory is full; the Go port is expected to decode these.

```
$ unarc t genuine/archives/genuine_ppmd_fill_r1.arc
UnArc 0.60RC unpacker. Testing archive: genuine_ppmd_fill_r1.arc
Testing text/1mb.txt (1048576 bytes)

ERROR: file text/1mb.txt failed CRC check
--- stderr

--- exit 2
```

### genuine_ppmd_fill_r2

PPMd filled its model memory. The 64-bit C build lays the model out differently from the 32-bit encoder, so its decoding diverges once the memory is full; the Go port is expected to decode these.

```
$ unarc t genuine/archives/genuine_ppmd_fill_r2.arc
UnArc 0.60RC unpacker. Testing archive: genuine_ppmd_fill_r2.arc
Testing text/1mb.txt (1048576 bytes)

ERROR: file text/1mb.txt failed CRC check
--- stderr

--- exit 2
```

### genuine_ppmd_fill_o16

PPMd filled its model memory. The 64-bit C build lays the model out differently from the 32-bit encoder, so its decoding diverges once the memory is full; the Go port is expected to decode these.

```
$ unarc t genuine/archives/genuine_ppmd_fill_o16.arc
UnArc 0.60RC unpacker. Testing archive: genuine_ppmd_fill_o16.arc
Testing text/1mb.txt (1048576 bytes)

ERROR: file text/1mb.txt failed CRC check
--- stderr

--- exit 2
```

### genuine_ppmd_fill_o4

PPMd filled its model memory. The 64-bit C build lays the model out differently from the 32-bit encoder, so its decoding diverges once the memory is full; the Go port is expected to decode these.

```
$ unarc t genuine/archives/genuine_ppmd_fill_o4.arc
UnArc 0.60RC unpacker. Testing archive: genuine_ppmd_fill_o4.arc
Testing text/1mb.txt (1048576 bytes)

ERROR: file text/1mb.txt failed CRC check
--- stderr

--- exit 2
```

### genuine_ppmd_fill_big

PPMd filled its model memory. The 64-bit C build lays the model out differently from the 32-bit encoder, so its decoding diverges once the memory is full; the Go port is expected to decode these.

```
$ unarc t genuine/archives/genuine_ppmd_fill_big.arc
UnArc 0.60RC unpacker. Testing archive: genuine_ppmd_fill_big.arc
Testing text/all.txt (1670043 bytes)

ERROR: file text/all.txt failed CRC check
--- stderr

--- exit 2
```

### genuine_dispack_exe

The C unarc (UnArc 0.60 based) has no dispack070 method; the Go port decodes it.

```
$ unarc t genuine/archives/genuine_dispack_exe.arc
UnArc 0.60RC unpacker. Testing archive: genuine_dispack_exe.arc
Testing unarc.exe (333312 bytes)

ERROR: unsupported compression method dispack070+lzma:459kb:normal:32
--- stderr

ERROR: compression method "dispack070" is not supported (if the setup ships CLS-*.dll for it, pass their folder with -cls)

--- exit 2
```

### genuine_dispack_mixed

The C unarc (UnArc 0.60 based) has no dispack070 method; the Go port decodes it.

```
$ unarc t genuine/archives/genuine_dispack_mixed.arc
UnArc 0.60RC unpacker. Testing archive: genuine_dispack_mixed.arc
Testing text/small.txt (262144 bytes)

ERROR: unsupported compression method dispack070+lzma:518kb:normal:32
--- stderr

ERROR: compression method "dispack070" is not supported (if the setup ships CLS-*.dll for it, pass their folder with -cls)

--- exit 2
```

### genuine_m1_mt4

The C unarc (UnArc 0.60 based) has no 4x4 method; the Go port decodes it.

```
$ unarc t genuine/archives/genuine_m1_mt4.arc
UnArc 0.60RC unpacker. Testing archive: genuine_m1_mt4.arc
Testing mix/empty.txt (0 bytes)
Testing mix/docs/readme.txt (163840 bytes)

ERROR: unsupported compression method rep:606kb:256+4x4:tor:3:606kb
--- stderr

ERROR: compression method "4x4" is not supported (if the setup ships CLS-*.dll for it, pass their folder with -cls)

--- exit 2
```

### genuine_m2_mt4

The C unarc (UnArc 0.60 based) has no 4x4 method; the Go port decodes it.

```
$ unarc t genuine/archives/genuine_m2_mt4.arc
UnArc 0.60RC unpacker. Testing archive: genuine_m2_mt4.arc
Testing mix/docs/readme.txt (163840 bytes)
Testing mix/docs/notes/notes.txt (49152 bytes)
Testing mix/src/main.cpp (65536 bytes)
Testing mix/data/random.bin (8192 bytes)

ERROR: unsupported compression method rep:9kb:256+4x4:tor:3:9kb:h64kb
--- stderr

ERROR: compression method "4x4" is not supported (if the setup ships CLS-*.dll for it, pass their folder with -cls)

--- exit 2
```

### genuine_m3_mt4

The C unarc (UnArc 0.60 based) has no 4x4 method; the Go port decodes it.

```
$ unarc t genuine/archives/genuine_m3_mt4.arc
UnArc 0.60RC unpacker. Testing archive: genuine_m3_mt4.arc
Testing mix/docs/readme.txt (163840 bytes)
Testing mix/docs/notes/notes.txt (49152 bytes)
Testing mix/src/main.cpp (65536 bytes)
Testing mix/data/random.bin (8192 bytes)

ERROR: unsupported compression method rep:9kb+4x4:tor:3:9kb:h64kb
--- stderr

ERROR: compression method "4x4" is not supported (if the setup ships CLS-*.dll for it, pass their folder with -cls)

--- exit 2
```

### genuine_m4_mt4

The C unarc (UnArc 0.60 based) has no 4x4 method; the Go port decodes it.

```
$ unarc t genuine/archives/genuine_m4_mt4.arc
UnArc 0.60RC unpacker. Testing archive: genuine_m4_mt4.arc
Testing mix/docs/readme.txt (163840 bytes)
Testing mix/docs/notes/notes.txt (49152 bytes)
Testing mix/src/main.cpp (65536 bytes)
Testing mix/data/random.bin (8192 bytes)

ERROR: unsupported compression method rep:9kb+4x4:tor:9kb:h64kb:c3
--- stderr

ERROR: compression method "4x4" is not supported (if the setup ships CLS-*.dll for it, pass their folder with -cls)

--- exit 2
```

### genuine_m5_mt4

The C unarc (UnArc 0.60 based) has no 4x4 method; the Go port decodes it.

```
$ unarc t genuine/archives/genuine_m5_mt4.arc
UnArc 0.60RC unpacker. Testing archive: genuine_m5_mt4.arc
Testing mix/docs/readme.txt (163840 bytes)

ERROR: unsupported compression method dict:276kb:80%:l8192:m400:s100+lzp:276kb:90%:65:h19:d1mb+4x4:b7mb:ppmd:8:96mb
--- stderr

ERROR: compression method "4x4" is not supported (if the setup ships CLS-*.dll for it, pass their folder with -cls)

--- exit 2
```

### genuine_m2x_mt4

The C unarc (UnArc 0.60 based) has no 4x4 method; the Go port decodes it.

```
$ unarc t genuine/archives/genuine_m2x_mt4.arc
UnArc 0.60RC unpacker. Testing archive: genuine_m2x_mt4.arc
Testing mix/docs/readme.txt (163840 bytes)

ERROR: unsupported compression method 4x4:tor:276kb:h2mb
--- stderr

ERROR: compression method "4x4" is not supported (if the setup ships CLS-*.dll for it, pass their folder with -cls)

--- exit 2
```

### genuine_4x4_lzma

The C unarc (UnArc 0.60 based) has no 4x4 method; the Go port decodes it.

```
$ unarc t genuine/archives/genuine_4x4_lzma.arc
UnArc 0.60RC unpacker. Testing archive: genuine_4x4_lzma.arc
Testing text/1mb.txt (1048576 bytes)

ERROR: unsupported compression method 4x4:lzma:1mb:normal:32
--- stderr

ERROR: compression method "4x4" is not supported (if the setup ships CLS-*.dll for it, pass their folder with -cls)

--- exit 2
```

### genuine_4x4_ppmd

The C unarc (UnArc 0.60 based) has no 4x4 method; the Go port decodes it.

```
$ unarc t genuine/archives/genuine_4x4_ppmd.arc
UnArc 0.60RC unpacker. Testing archive: genuine_4x4_ppmd.arc
Testing text/1mb.txt (1048576 bytes)

ERROR: unsupported compression method 4x4:b256kb:ppmd:8:16mb
--- stderr

ERROR: compression method "4x4" is not supported (if the setup ships CLS-*.dll for it, pass their folder with -cls)

--- exit 2
```

