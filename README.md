# go-unarc

A Go port of `unarc`, the FreeArc unpacker ([xredor/unarc](https://github.com/xredor/unarc), UnArc 0.60 with
fixes for archives made by FreeArc 0.67): the same commands, options, environment variables,
output, exit codes and compression methods, with safe parsing of untrusted archives and
the method chains run as goroutines connected by pipes.

## Build

```
git lfs install                  # once per machine; the test archives (*.arc) are Git LFS files
git clone https://github.com/n0madic/go-unarc && cd go-unarc
go build -o go-unarc ./cmd/unarc
```

or, without a clone, `go install github.com/n0madic/go-unarc/cmd/unarc@latest` (installs `unarc`).

Needs Go 1.26. Dependencies: the standard library, `golang.org/x/crypto` (Blowfish, Twofish) and
`golang.org/x/sys`. The program itself does not need the archives or Git LFS; a clone made without git-lfs
holds pointer files instead of them, and `git lfs pull` fetches the archives (the tests that use them stop with
that hint until then).

## Use

```
go-unarc l -p'password' archive.arc
go-unarc x -o+ -p'password' -cls/path/to/setup/dlls -dpout archive.arc
```

Commands `l v e x t`; options `--noarcext -o+ -o- -dp{dir} -cls{dir} -p{pwd} -w{dir} --`; variables
`UNARC_PASSWORD UNARC_CLS_DIR UNARC_WINE UNARC_CLSHOST UNARC_SREP UNARC_CLS_KEEP UNARC_DEBUG UNARC_BLOCKS`,
`TMPDIR`, `WINEDEBUG`, `WINEPREFIX`. `arc.ini` is read from the folder of the executable. Run it without
arguments for the list of commands and options.

- `-p{pwd}` or `UNARC_PASSWORD`: the password of an encrypted archive.
- `-cls{dir}` or `UNARC_CLS_DIR`: the folder with a setup's `CLS-*.dll` codecs. Each such stage (but `msc`, see
  below) runs through `clshost.exe` under Wine (`UNARC_WINE`, `UNARC_CLSHOST`). Use only trusted DLLs;
  Wine is not a sandbox. `clshost.exe` is a small Windows
  program that loads one CLS DLL and runs it from file to file; its source is `tools/clshost/clshost.c`, and
  `tools/clshost/build.sh` builds it into `.build/clshost.exe` (needs mingw-w64). It is looked up as
  `UNARC_CLSHOST`, else beside `go-unarc`, else in the CLS folder.
- `srep` is built in (SREP formats v1–v4, with MD5, SHA-1, SHA-512, VHASH and SipHash block checksums). Set
  `UNARC_SREP` to a binary path to use the external decoder instead. As with other built-in methods, an
  explicit `srep` definition in `arc.ini` takes precedence.
- Built-in `srep` uses `-w` (or `TMPDIR`) for older output needed by long-distance matches. Index-LZ (v4)
  also saves its input there if it is not seekable. The built-in decoder accepts blocks up to 8 MiB and
  up to 1,048,576 pending matches; use `UNARC_SREP` if you need a different decoder for an archive.
- Plugin stages spill to temp files under `TMPDIR` (or `-w`): allow disk space for the whole solid block.

### Resource limits

The defaults can be raised for trusted archives. Values are positive decimal integers; unset, zero, and
invalid values use the defaults. `MB` here means MiB (1,048,576 bytes).

| Variable | Default | Applies to |
| --- | ---: | --- |
| `UNARC_MAX_CONTROL_MB` | 64 | Packed and unpacked size of each footer or directory block |
| `UNARC_MAX_DECODER_MB` | 1024 | Large buffers and models in native decoders; not a process RSS limit |
| `UNARC_MAX_TEMP_MB` | 65536 | Live decoder scratch files, including the logical size of SREP history; not extracted files |
| `UNARC_MAX_KDF_ITERATIONS` | 1000000 | PBKDF2 iterations per password-derived key |

The temporary storage limit is checked before Go writes scratch files. External helpers' outputs are checked
after they exit; use a quota-limited work directory or OS isolation to enforce a disk limit on the helpers
themselves. `UNARC_CLS_KEEP` leaves debug files behind, so those files also need separate disk management.

For `arc.ini` external decompressors, a `{options}` or `{…option…}` placeholder must be a separate field
after the executable. Archive options used by that field must be listed in `allowedopts` in the same section
(for example, `allowedopts = m48, r1`). `default` still supplies local options when the archive supplies none.
Only enable external programs and options you trust.

Methods: `storing tempfile lzma exe delta rep lzp dict ppmd tor grzip mm tta 4x4 dispack070 msc`, `aes blowfish
twofish serpent` (any key size, `/ctr` or `/cfb`), `arc.ini` external compressors and `pmm`, `srep`
and `CLS-*.dll` (under Wine).

`msc` is the MSC plugin of ProFrager (`CLS-MSC.dll`), which stores the PCM WAV files inside game archives (BIGF)
as TAK audio. It is decoded natively, with no DLL and no Wine, for `msc:tak=N:bmp=0:dxt=0` (N not 0), the
parameters of the streams it was checked with; with other parameters (the image models `bmp` and `dxt`) the
DLL is used if `-cls` has it. The format has no public description: package `internal/codec/msc` documents what
was read from the DLL's output, and `internal/codec/tak` is a port of FFmpeg's TAK decoder.

## Differences from the C unarc

Fixed defects:

- The overwrite question accepts `y/n/a/s/q` (the C one rejected every answer); end of input means `n`.
- `--` ends the options; `--noarcext` turns off adding `.arc` to a missing archive name.
- Files are staged alongside their destination and published only after their size, CRC and close succeed.
  A failed overwrite leaves the original intact. Only files written here get their modification time set.
  Publishing without permission to overwrite uses a hard link so a concurrently created file is never
  replaced. On filesystems without hard links, extract to an empty directory with `-o+` if needed.
- A name with a `..` component or an absolute path (`/x`, `\x`, `C:`) stops extraction with
  `ERROR: unsafe path in archive: <name>`, exit code 2. Output operations are rooted in the destination;
  a symlink in the output tree cannot redirect them outside that tree. A directory creation failure is an error.
- Encryption: `k/i/s/c` lengths are checked before decoding (the C one overflowed its stack), the check
  code is compared on exactly 2 bytes; bad parameters are reported as corrupt data. A key size or number of
  rounds the cipher rejects passes the data through in CTR mode, as in C (FreeArc 0.67 stored such data
  unencrypted), and is corrupt data in CFB mode (the C one crashes or produces garbage).
- PPMd keeps the 32-bit memory layout, so blocks that filled the model's memory decode.
- `4x4` (FreeArc 0.67's multi-threaded method, its default `-m1`..`-m5` on more than one CPU) is decoded,
  its blocks in parallel on one goroutine per CPU, fewer when their memory would exceed the limit that
  also decides where `tempfile` goes (3/4 of RAM, capped at about 3 GiB), as FreeArc does; the C unarc does not
  have the method. The resource limits above are separate from this scheduling estimate.
- `dispack070` (FreeArc 0.67's x86 code filter, `exe2`, which repacks put before `delta+lzma`) is decoded;
  the C unarc does not have it either. Where the C code trusts the stream (stored record lengths, sizes of
  the filtered streams, code shorter than its record says) the Go one reports corrupt data.
- An unfiltered `t` lets each decoder finish and checks for surplus decoded bytes. A test with selected
  filenames, or extraction, can still stop early when it has the requested files.
- External compressors run without a shell (`$$arcdatafile$$` is passed literally), from the folder of
  the executable or `PATH`; `Errorlevel=` prints the program's exit code.

Also: the first line of the output is `UnArc unpacker`, without the C one's version and link; with
`UNARC_DEBUG`, a chain that fails on corrupt data prints why on stderr, naming the method
(`Decompress error: lzma: compressed data corrupted: ...`), without dumping keys or decrypted bytes;
LZMA refuses `lc>8`, `lp>4`, `pb>4` and an
empty dictionary; a CLS DLL path is passed to Wine as an absolute path. Not supported, as in the C unarc:
`lzma2`, `precomp`, SFX archives.

## Tests

```
go vet ./... && go test ./... && go test -race ./...
go test -run XXX -fuzz FuzzOpen ./internal/arc   # every decoder has a FuzzDecode
```

- `testdata/archives`, `testdata/golden`: archives made by `scripts/mkfixtures` (streams from FreeArc's
  own encoders, built by `scripts/fagen/build.sh` into `.build/fagen`) and the C unarc's output for each
  command line (compared with the Go header in place of C's, as `scripts/diffcheck.sh` does). `internal/cli` `TestGolden` runs them all and fails on a method that is not registered.
- `testdata/genuine`: archives made by FreeArc 0.67 itself (`scripts/genuine/build.sh`: the Windows build
  under Wine in an i386 VM in Docker) and the C unarc's output for them; see its README. The archives
  the C unarc fails have no golden runs: the six `genuine_ppmd_fill_*` (CRC errors of the 64-bit PPMd
  layout), the eight 4x4 ones (`genuine_*_mt4`, `genuine_4x4_*`) and the two dispack070 ones
  (`genuine_dispack_*`). `TestGenuineNoGolden` tests them.
- All `*.arc` files are Git LFS files (`.gitattributes`).
- `internal/codectest`: what the codec tests share (running fagen, test writers, test text). Codec tests
  that need `.build/fagen` skip without it.
- `testdata/srep`: reference streams made by SREP 3.93a. The codec and archive tests do not need `srep`;
  they exercise matches across blocks, block checksums, and `t` / `x` on an `srep+lzma` chain.
- `internal/codec/tak`, `internal/codec/msc`: there is no TAK or MSC encoder to make fixtures with, so the tests
  build streams by hand (`internal/codec/tak/taktest`, which shares no code with the decoder) and compare
  with results worked out from the definitions. `TestOracle` of `msc` compares the decoding of a whole real
  stream with the DLL's (`MSC_PACKED`: the input of the msc stage of an archive, which `UNARC_CLS_KEEP=1` keeps
  for a build that still sends the stage to the DLL, as this one no longer does; `MSC_UNPACKED`: what
  `clshost.exe` makes of it under Wine; skipped without them, the data is not free).

### Against the C unarc

The scripts below compare with, or regenerate data from, the C unarc, which is not part of this repository.
`diffcheck.sh` requires `timeout` or `gtimeout` (GNU coreutils); a timed-out or failed launch is not a match.
Two variables say where it is; nothing is looked up by relative path:

- `C_UNARC`: the C unarc binary (the oracle of `diffcheck.sh`, `benchcodec -archives`, `mkfixtures` and the
  `golden` step of `scripts/genuine/build.sh`);
- `FREEARC_SRC`: the folder with its sources, `Environment.cpp` and `Compression/` (`scripts/fagen/build.sh`
  compiles FreeArc's encoders from them; `mkfixtures` and `scripts/genuine` take their text inputs from them,
  the latter from the git `HEAD` of a checkout).

The recorded output in `testdata` is from xredor/unarc at `adc333d` with local changes (password and CLS plugin
support, FreeArc 0.67's key decoding, 64-bit fixes of PPMd, GRZip, TTA, MM and Dict) that are not published.
<!-- TODO: link the C unarc with these changes once it is published, and pin the commit here. -->

```
scripts/fagen/build.sh                      # FREEARC_SRC=... builds .build/fagen; without it, tests that need it skip
UNARC_PASSWORD=test-password C_UNARC=... scripts/diffcheck.sh   # Go against the C unarc on every fixture: l, v, t, x
go run ./scripts/benchcodec                 # CPU time of each codec against FreeArc's C library (needs .build/fagen)
go build -o .build/go-unarc ./cmd/unarc && C_UNARC=... go run ./scripts/benchcodec -archives big.arc
                                            # "t" with the C unarc and the Go one
```

## License

GPL-2.0-only, see [LICENSE](LICENSE). The port derives from GPL code (see the credits below), which rules out a
more permissive license. The TAK decoder derives from FFmpeg's (LGPL 2.1 or later), used here under the GPL.

## Credits

go-unarc is a port of FreeArc's decompression code and carries the credits of that code: the container and the
command line of `unarc` (FreeArc by Bulat Ziganshin, GPL) and the codecs it links: LZMA (Igor Pavlov, public
domain), PPMd var.I (Dmitry Shkarin, public domain), Tornado (Bulat Ziganshin and Joachim Henke, GPL), GRZip
(Ilya Grebnov, LGPL), TTA (Alexander Djourik and Pavel Zhilin, GPL), Serpent (Brian Gladman, through libmcrypt
and LibTomCrypt), the DisPack x86 filter, and FreeArc's own delta, rep, lzp, dict and mm filters. The comments
cite the C sources they follow by file name (`Model.cpp`, `C_Tornado.cpp`, ...). The text in some test archives
comes from those sources.

Not from FreeArc: `internal/codec/tak` is a port of the TAK decoder of FFmpeg (`takdec.c`, `tak.c`, `takdsp.c`,
Copyright (c) 2012 Paul B Mahol, LGPL 2.1 or later); TAK (Tom's lossless Audio Kompressor) is by Thomas Becker.
`msc` follows the output of the MSC plugin of ProFrager (FragSoft), whose source is not public.
`srep` follows the SREP format by Bulat Ziganshin; block verification uses Ted Krovetz and Wei Dai's VHASH
([draft-krovetz-vmac-01](https://datatracker.ietf.org/doc/html/draft-krovetz-vmac-01), public-domain reference
implementation) and Aumasson and Bernstein's SipHash-2-4.
