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
  below) runs through `clshost.exe` under Wine (`UNARC_WINE`, `UNARC_CLSHOST`). `clshost.exe` is a small Windows
  program that loads one CLS DLL and runs it from file to file; its source is `tools/clshost/clshost.c`, and
  `tools/clshost/build.sh` builds it into `.build/clshost.exe` (needs mingw-w64). It is looked up as
  `UNARC_CLSHOST`, else beside `go-unarc`, else in the CLS folder.
- `srep` stages always run the native `srep` binary: `UNARC_SREP`, else the `srep` beside `go-unarc`, else `PATH`.
- Plugin stages spill to temp files under `TMPDIR` (or `-w`): allow disk space for the whole solid block.

Methods: `storing tempfile lzma exe delta rep lzp dict ppmd tor grzip mm tta 4x4 dispack070 msc`, `aes blowfish
twofish serpent` (any key size, `/ctr` or `/cfb`), `arc.ini` external compressors and `pmm`, `srep` (native)
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
- Only the files actually written get their modification time set.
- A name with a `..` component or an absolute path (`/x`, `\x`, `C:`) stops extraction with
  `ERROR: unsafe path in archive: <name>`, exit code 2; nothing is written outside the output folder.
- Encryption: `k/i/s/c` lengths are checked before decoding (the C one overflowed its stack), the check
  code is compared on exactly 2 bytes; bad parameters are reported as corrupt data. A key size or number of
  rounds the cipher rejects passes the data through in CTR mode, as in C (FreeArc 0.67 stored such data
  unencrypted), and is corrupt data in CFB mode (the C one crashes or produces garbage).
- PPMd keeps the 32-bit memory layout, so blocks that filled the model's memory decode.
- `4x4` (FreeArc 0.67's multi-threaded method, its default `-m1`..`-m5` on more than one CPU) is decoded,
  its blocks in parallel on one goroutine per CPU, fewer when their memory would exceed the limit that
  also decides where `tempfile` goes (3/4 of RAM), as FreeArc does; the C unarc does not have the method.
- `dispack070` (FreeArc 0.67's x86 code filter, `exe2`, which repacks put before `delta+lzma`) is decoded;
  the C unarc does not have it either. Where the C code trusts the stream (stored record lengths, sizes of
  the filtered streams, code shorter than its record says) the Go one reports corrupt data.
- Every decoder checks its bounds: damaged data is an error, not a crash or a hang.
- External compressors run without a shell (`$$arcdatafile$$` is passed literally), from the folder of
  the executable or `PATH`; `Errorlevel=` prints the program's exit code.

Also: the first line of the output is `UnArc unpacker`, without the C one's version and link; with
`UNARC_DEBUG`, a chain that fails on corrupt data prints why on stderr, naming the method
(`Decompress error: lzma: compressed data corrupted: ...`); LZMA refuses `lc>8`, `lp>4`, `pb>4` and an
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
- `internal/codec/tak`, `internal/codec/msc`: there is no TAK or MSC encoder to make fixtures with, so the tests
  build streams by hand (`internal/codec/tak/taktest`, which shares no code with the decoder) and compare
  with results worked out from the definitions. `TestOracle` of `msc` compares the decoding of a whole real
  stream with the DLL's (`MSC_PACKED`: the input of the msc stage of an archive, which `UNARC_CLS_KEEP=1` keeps
  for a build that still sends the stage to the DLL, as this one no longer does; `MSC_UNPACKED`: what
  `clshost.exe` makes of it under Wine; skipped without them, the data is not free).

### Against the C unarc

The scripts below compare with, or regenerate data from, the C unarc, which is not part of this repository.
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
