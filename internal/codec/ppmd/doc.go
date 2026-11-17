// Package ppmd implements the ppmd method: PPMd var.I rev.1 by Dmitry Shkarin with Dmitry
// Subbotin's carryless range coder, as used by FreeArc ("ppmd:order:mem[:r[N]]").
//
// # Stream format
//
// There is no header: the compressed stream is the output of the range coder alone. The coder keeps
// 32-bit low, range and code registers; the decoder starts with the first four bytes as code and
// reads one byte per normalisation step. Symbols are coded in an order-N context tree: a symbol is
// found in the longest context that has seen it or is escaped down the suffix chain, and the
// order-0 context of 256 symbols ends in the escape that marks the end of the data. The encoder
// finishes by flushing four bytes of low.
//
// The method parameters (order 2..128, model memory and the restoration method applied when the
// memory is exhausted: 0 restart, 1 cut off, 2 freeze) are not stored in the stream, they come from
// the method string.
//
// # 32-bit memory layout
//
// Real archives were written by 32-bit FreeArc, whose model lives in 12-byte allocation units
// addressed by 32-bit pointers; when the model memory runs out, the position of the restart or cut-off
// depends on that layout. This package therefore emulates it: one []byte heap of exactly the model
// size, 12-byte units and uint32 offsets in place of pointers (0 is NULL), the way 7-Zip's Ppmd8
// does it. The 64-bit builds of the C decoder use bigger structures and diverge from the real
// streams once the memory is filled.
package ppmd
