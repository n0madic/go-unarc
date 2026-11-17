// Package grzip implements the grzip method: a decoder for the GRZipII block-sorting compressor
// (Grebnov Ilya) as shipped in FreeArc.
//
// # Stream format
//
// A stream is a sequence of independent blocks, each a 28-byte header followed by Len payload bytes.
// The header is seven little-endian int32 fields:
//
//	+0  Size    number of bytes the block decodes to (at most 8 MiB - 512)
//	+4  Mode    -1 stored block, -2 delta/reorder container, else a flag set (below)
//	+8  Aux     see below
//	+12 FBP     first-byte position of the BWT / ST4 transform
//	+16 Len     payload length in bytes
//	+20 0       reserved, must be zero
//	+24 0       reserved, must be zero
//
// Mode >= 0 is a combination of
//
//	bit 1       ST4 (order-4 Schindler transform) instead of BWT
//	bit 2       MTF + arithmetic coder instead of WFC (weighted frequency count) + arithmetic coder
//	bits 8-15   LZP hash table size, log2
//	bits 16-30  LZP minimum match length (bits 8-30 all zero: no LZP)
//
// and the other bits only matter to the encoder. The decoder of such a block runs the encoder's stages
// backwards: arithmetic decoding (the run-length/rank models of MTF_Ari.c or WFC_Ari.c) yields the transformed
// text padded to a multiple of 8 bytes (Aux is its unpadded length), the inverse BWT or ST4 restores the LZP
// output, and the LZP decoder expands it to Size bytes. FBP selects the BWT flavour too: bit 30 set means the
// "strong" sort, which numbers the rows differently.
//
// A stored block (Mode -1) holds Len bytes of payload; Aux 0 means they are the data, anything else means the
// payload is LZP output whose parameters are packed in Aux like in Mode. A container (Mode -2) holds two (Aux
// odd) or four (Aux even) complete blocks that a reordering filter (Aux 1 and 2: byte planes of 2 or 4 byte
// records, Aux 3 and 4: planes of zigzag deltas of 16 or 32 bit words) merges back into Size bytes.
//
// # Decoding
//
// Blocks are independent, so Decode reads them one by one and hands them to a pool of workers, as the C
// MTCompressor did (package parallel, shared with 4x4); the decoded blocks are written in stream order and at
// most workers+workers/2+1 blocks are in flight.
//
// Where the C decoder trusts the stream (sizes, FBP, ranks, run lengths, hash table size) this port checks it
// and returns method.ErrBadData instead of reading or writing out of bounds.
package grzip
