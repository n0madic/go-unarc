// Package tak decodes TAK (Tom's lossless Audio Kompressor) streams: the "tBaK" files, and the frame data
// inside other containers. It is a port of FFmpeg's TAK decoder (libavcodec/takdec.c, tak.c, takdsp.c and the
// metadata parsing of libavformat/takdec.c, Copyright (c) 2012 Paul B Mahol, LGPL 2.1 or later) to Go; the
// arithmetic keeps the 32-bit wrap-around of the C code so that the output is the same sample for sample.
//
// A stream is the magic "tBaK", metadata blocks (type byte, 24-bit size, payload, CRC-24) up to an end block,
// and then frames. A frame holds the samples of every channel for a fixed duration: a header, per-channel
// residues coded with adaptive Rice-like codes plus LPC filters of up to 256 taps, a decorrelation of stereo
// (or multichannel) pairs, and a CRC-24. It does not carry its length: the decoder finds the end by decoding.
//
// Mono, stereo and multichannel (up to six channels) streams of 8, 16 and 24 bits per sample are decoded, to
// interleaved little-endian PCM as a WAV file holds it. The multichannel paths follow the C code but have no
// test stream: there is no TAK encoder to make one.
package tak
