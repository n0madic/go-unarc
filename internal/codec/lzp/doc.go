// Package lzp implements the lzp method: a fast LZ preprocessor by Dmitry Shkarin and Ilya Grebnev that predicts
// the next bytes from a hash of the previous ones.
//
// The stream is a sequence of blocks, each preceded by an int32 size (the stream ends cleanly between blocks):
//
//	size < 0   a stored block of -size bytes, copied as is
//	size >= 0  a compressed block of size bytes that decodes to at most BlockSize bytes
//
// A compressed block is read from both ends. The first 12 bytes are copied verbatim to prime the hash. After
// them, from the front, comes the main stream: every byte is a literal except the flag 0xB5, which stands for
// a match when the hash table predicts one. The block's end holds one record per flag, taken from the last byte
// backwards: 255 means "the flag was a literal flag byte after all", anything else starts a match length that
// continues through zero bytes, each adding 254 (see lengths in decodeBlock).
//
// The hash table holds, for every hash of the last 4 bytes (and the 4 before them), the position after the
// latest occurrence. The decoder must update it exactly like the encoder: a position is stored every n1-th
// time (n1 grows when matches are found at long distances), and while copying a match, every 2*n1+1 bytes.
package lzp
