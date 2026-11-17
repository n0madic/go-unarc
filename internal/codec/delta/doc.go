// Package delta implements the delta method: a filter for tables of fixed-size binary records.
//
// The encoder looks for tables (runs of equal-sized records) in its input, reorders the columns of every table
// so that constant ones come first and subtracts each record from the next one in the columns that vary.
// The decoder reverses both steps. The stream is a sequence of blocks, all integers are little-endian int32:
//
//	DataSize   bytes in Data (the stream ends cleanly where the next DataSize would start)
//	TableSize  bytes in each of the three descriptor arrays below (4 per table)
//	TSkip      TableSize bytes: uint32 per table, bytes between the end of the previous table and this one
//	TType      TableSize bytes: uint32 per table, 1<<N plus one bit per column that is NOT subtracted
//	TRows      TableSize bytes: uint32 per table, number of records
//	Data       DataSize bytes: the filtered data with the tables in place
//
// N is the record size (at most 30 bytes). Subtraction runs bytewise from the lowest address with a carry that
// is kept only across adjacent subtracted columns, so decoding adds the previous record back the same way.
package delta
