// Package mm implements the mm method: a preprocessor for raw multimedia data (PCM audio, bitmaps) that replaces
// every sample by its difference to the previous sample of the same channel, which compressors after it model
// much better.
//
// The stream starts with a flags byte:
//
//	0   the rest of the stream is stored: copied as is
//	1   data in the difference form, followed by
//	      numChan   byte: number of channels
//	      wordSize  byte: bits per sample of one channel; the samples take (wordSize+7)/8 bytes
//	      offset    int32: size of the file header, which comes next and is copied as is
//	      padding   zero bytes that move the data to a multiple of the sample size (numChan * bytes per
//	                sample) counted from the start of the stream; the C decoder reads them into its table of
//	                previous samples, so they are the initial values of the first channels
//	      samples   until the end of the stream, in little-endian byte order
//
// Any other flags value (the C code reserves bits for byte and word reordering that were never finished) is
// rejected, and so is a stream with no channels or no bits in a sample, which the C decoder cannot handle
// either. The decoder adds the previous sample of the channel to every sample, modulo 2^8, 2^16, 2^24 or 2^32
// for samples of 1 to 4 bytes. Samples of more than 4 bytes were never filtered and pass through, and so does
// a partial sample at the end of the stream. The method's own parameters only steer the encoder (channel
// detection), the decoder needs none.
package mm
