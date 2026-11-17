// Package tta decodes the "tta" method of FreeArc: a lossless audio compressor derived from the True Audio
// codec (ttaenc 2.0 by Alexander Djourik and Pavel Zhilin) that FreeArc applies to any data it takes for
// samples (WAV files, raw PCM). It ports tta_decompress of unarc (Compression/MM/tta.cpp, entropy.cpp,
// filters.cpp).
//
// # Method string
//
// "tta" takes the parameters of the encoder: m (level), s (skip WAV header detection), f (floating point),
// c, w and o (channels, bits per sample, offset of the audio data), r (raw mode) and the unnamed "channels*bits"
// with an optional "f" after it. The decoder reads everything from the stream, so Parse only checks that the
// string is one the C parser accepts.
//
// # Stream format
//
// All numbers are little-endian. The stream starts with a 4-byte header:
//
//	level      0 = stored, 1..3 = strength of the predictor
//	flags      bit 0: floating-point samples; bits 1..7: raw mode (0 = entropy coded, 1 = no entropy coder)
//	channels   number of channels
//	wordSize   bits per sample; the sample size is (wordSize+7)/8 bytes: 1..3 for integers, 4 for floats
//
// A level 0 stream continues with the original data and nothing else. Otherwise a 4-byte length and that many
// bytes follow (the head of the original file in front of the audio data, which is not compressed), and then
// frames until the end of the stream. A frame covers up to 2^18 samples of every channel:
//
//	uint32 bytesRead     original size of the frame in bytes
//	uint32 compressed    size of the bit array; 0 means that the bytesRead original bytes follow (absent in raw mode)
//	[compressed bytes]   one bit array holding every channel in turn, see entropy.go
//	[rest bytes]         bytesRead % (channels*sampleSize) trailing bytes of the data as they are
//
// In raw mode the compressed size is missing and every channel is stored as bytesRead/(channels*sampleSize)
// int32 values, the output of the predictor.
//
// A decoded channel is turned back into samples by the filters of filters.go and the channels are recombined
// (combine.go): the encoder replaces the channels of integer samples by their differences, and splits a float
// into a channel of the sign-less exponent and top mantissa bits and one of the signed, bit-reversed rest.
//
// # Differences from the C decoder
//
//   - A stream that ends right after a frame is a success. The C decoder reports FREEARC_ERRCODE_READ there; unarc
//     does not see it, because it stops the decoder (ErrStop here) as soon as the last file is complete.
//   - Whatever makes the C decoder read outside its buffers is ErrBadData: more samples than the bit array
//     holds bits, a Rice parameter beyond 33, a frame size of zero channels or zero bits per sample (C divides by
//     zero), a frame or compressed size above 1 GB (C prints a message and goes on with it).
//   - Sizes taken from the stream do not allocate before the data behind them arrives; a bit array needs at least
//     one bit per sample, which bounds the sample buffer of a frame.
//
// The C encoder itself is not reliable for frames whose bit array outgrows its initial 1 MB: what it writes
// there depends on uninitialised memory (two runs on the same input give different streams, and the C decoder
// does not give the input back from some of them). Such streams decode to garbage in the C decoder, and here either
// to garbage or to ErrBadData.
package tta
