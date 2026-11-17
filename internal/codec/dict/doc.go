// Package dict implements the dict method: dictionary replacement for text, which swaps frequent words for one
// or two byte codes so that the compressors after it see a shorter, less redundant text.
//
// The stream is a sequence of blocks, each preceded by an int32 size (the stream ends cleanly between blocks):
//
//	size < 0   a stored block of -size bytes, copied as is
//	size >= 0  a block of size bytes that decodes to at most BlockSize bytes; it carries its own dictionary
//
// A block starts with the dictionary:
//
//  1. 256 bytes: the length of the word coded by each byte value. 0 means the byte stands for itself,
//     1 means that the byte starts two-byte codes, anything else is the length of a one-byte-coded word.
//  2. 256 bytes for every byte value marked 1: the number of leading bytes that the two-byte code's word
//     shares with the previous two-byte word (words are sorted, so this is mostly large).
//  3. The text of the one-byte-coded words, in byte value order.
//  4. A separator byte, then the remaining text of every two-byte-coded word, each ended by the separator.
//  5. The escape byte: it gets 256 two-byte codes "escape, x" meaning x, so that bytes that gave their code to
//     a word can still occur in the text.
//
// The rest of the block is the text, where every code is replaced by its word.
package dict
