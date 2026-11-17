// Package msc decodes the msc method: the MSC plugin of FragSoft's ProFrager (CLS-MSC.dll), a preprocessor
// that finds the PCM WAV files inside a stream of game archives (BIGF, as in C&C Generals) and compresses their
// samples with the TAK audio coder, the rest of the stream passing through. Its output is the original byte
// stream, which the stages after it (srep, lzma) saw as incompressible audio.
//
// The method has no published description; the format below is read from the plugin's output and verified
// byte for byte against the DLL's decoding of an 857 MB stream with 5623 audio files. The stream is
//
//	"MSC " and a little-endian uint32 1
//	items, until the end of the stream
//
// An item starts with a tag byte: its low nibble is the kind, its high nibble the number of bytes of the length
// field minus one; the little-endian length follows, then that many bytes of payload.
//
//	kind 0   raw data: the payload is copied to the output
//	kind 3   a PCM WAV file with its samples in TAK. The payload is
//	           00 2c 00 00 00 00 00     (44 as a uint32 after a zero byte, then two zero bytes)
//	           the RIFF size            (4 bytes, "RIFF" and "WAVE" are not stored)
//	           00 00 00 00
//	           the fmt chunk            (24 bytes, from "fmt " to the end of the format description)
//	           "data" and the size of the sample data
//	           a TAK stream "tBaK"...   (package tak) up to the end of the payload
//	         and gives the 44-byte WAV header followed by the decoded samples. What follows the sample data in the
//	         file (cue, LIST and bext chunks) is a raw item after it, as is every file that is not a plain 44-byte
//	         header PCM WAV (ADPCM, extra chunks) and everything between the files.
//
// The plugin has more models: its parameters name bmp and dxt, the image models, which this decoder does not
// know; Parse takes only the parameters of the data it was checked with (tak enabled, bmp and dxt off) and
// leaves the others to the DLL (package plugin). A stream with these parameters that holds an item of another
// kind is not passed on to the DLL: it is reported as corrupt data.
package msc
