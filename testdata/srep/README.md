# SREP fixtures

`streams.txt` contains base64-encoded streams produced by SREP 3.93a (`-v0 -l32 -b8kb`),
round-tripped through `srep -d`. The input is `b"FreeArc/SREP-0123456789abcdef!?--" * 615 + b"tail!"`
(three blocks, with LZ matches within and across them). `empty` uses an empty input.

| Fixture | Additional options |
| --- | --- |
| `v1` | `-m3o -hash=md5` |
| `v2` | `-m5o -hash=sha1` |
| `v3` | `-m3f` (legacy macOS arm64 VHASH) |
| `v3le` | `-m3f` (standard VHASH, build with `-DVMAC_ARCH_BIG_ENDIAN=0`) |
| `v4` | `-m3` (legacy macOS arm64 VHASH) |
| `sip` | `-m3f -hash=siphash` |
| `sha512` | `-m3f -hash=sha512` |
| `none` | `-m3f -hash-` |
| `empty` | `-m3 -hash=md5` |

`indexed.lzma.b64` is `v4` passed through `fagen c lzma:1mb` for the archive-level chain test.
To repeat a differential test on larger data, run
`SREP_ORACLE=/absolute/path/to/srep go test -run TestOracle ./internal/codec/srep`.
