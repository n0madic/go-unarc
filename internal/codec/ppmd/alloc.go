package ppmd

import "encoding/binary"

// The sub-allocator of PPMd var.I (SubAlloc.hpp) over one []byte heap. Pointers of the C code are
// uint32 offsets into the heap, 0 is NULL. The heap holds, from low to high addresses: the text
// area (raw symbols that successors point at), the units area growing upwards from unitsStart
// (loUnit marks its end) and the contexts area growing downwards from the end of the heap
// (hiUnit marks its start). Everything is organised in 12-byte units like the 32-bit build does,
// so memory runs out at exactly the same moment as it did for the encoder.

// blkNode is the head of a free list: a count of blocks (Stamp of BLK_NODE) and the first block.
type blkNode struct {
	stamp uint32
	next  uint32
}

// allocator is the heap together with the free lists and area boundaries.
type allocator struct {
	heap       []byte
	size       uint32 // SubAllocatorSize
	heapStart  uint32
	pText      uint32
	unitsStart uint32
	loUnit     uint32
	hiUnit     uint32
	glueCount  uint32
	glues      int // number of glueFreeBlocks calls, for the tests
	bList      [nIndexes]blkNode
}

// newAllocator reserves a heap of size bytes, reusing the storage of old if it has the same size. The
// offsets stay below 2^32 and offset 0 never points into the model, which is why the model starts a few
// bytes into the buffer; the extra unit at the end lets GlueFreeBlocks peek at the unit after the last
// block like the C code does.
func newAllocator(size uint32, old []byte) allocator {
	align := 4 - size&3
	heap := old
	if n := uint64(align) + uint64(size) + unitSize; uint64(len(heap)) != n {
		heap = make([]byte, n)
	}
	return allocator{heap: heap, size: size, heapStart: align}
}

func (a *allocator) get16(o uint32) uint16 { return binary.LittleEndian.Uint16(a.heap[o:]) }
func (a *allocator) get32(o uint32) uint32 { return binary.LittleEndian.Uint32(a.heap[o:]) }
func (a *allocator) set16(o uint32, v uint16) {
	binary.LittleEndian.PutUint16(a.heap[o:], v)
}
func (a *allocator) set32(o uint32, v uint32) {
	binary.LittleEndian.PutUint32(a.heap[o:], v)
}

// insertBlk puts the block at pv with nu units on free list i (BLK_NODE::insert).
func (a *allocator) insertBlk(i uint32, pv, nu uint32) {
	b := &a.bList[i]
	a.set32(pv+blkNext, b.next)
	b.next = pv
	a.set32(pv+blkStamp, ^uint32(0))
	a.set32(pv+blkNU, nu)
	b.stamp++
}

// removeBlk takes the first block off free list i (BLK_NODE::remove); the list must not be empty.
func (a *allocator) removeBlk(i uint32) uint32 {
	b := &a.bList[i]
	p := b.next
	b.next = a.get32(p + blkNext)
	b.stamp--
	return p
}

func (a *allocator) splitBlock(pv, oldIndx, newIndx uint32) {
	udiff := uint32(indx2Units[oldIndx]) - uint32(indx2Units[newIndx])
	p := pv + unitSize*uint32(indx2Units[newIndx])
	i := uint32(units2Indx[udiff-1])
	if uint32(indx2Units[i]) != udiff {
		i--
		k := uint32(indx2Units[i])
		a.insertBlk(i, p, k)
		p += unitSize * k
		udiff -= k
	}
	a.insertBlk(uint32(units2Indx[udiff-1]), p, udiff)
}

// usedMemory is GetUsedMemory: heap size minus the free gap, the text area and all free lists.
func (a *allocator) usedMemory() uint32 {
	ret := a.size - (a.hiUnit - a.loUnit) - (a.unitsStart - a.pText)
	for i := range a.bList {
		ret -= unitSize * uint32(indx2Units[i]) * a.bList[i].stamp
	}
	return ret
}

func (a *allocator) initSubAllocator() {
	a.bList = [nIndexes]blkNode{}
	a.pText = a.heapStart
	a.hiUnit = a.pText + a.size
	diff := unitSize * (a.size / 8 / unitSize * 7)
	a.loUnit = a.hiUnit - diff
	a.unitsStart = a.loUnit
	a.glueCount = 0
}

// glueFreeBlocks merges adjacent free blocks and redistributes them over the free lists.
func (a *allocator) glueFreeBlocks() {
	a.glues++
	if a.loUnit != a.hiUnit {
		a.heap[a.loUnit] = 0
	}
	// First pass: chain every free block, absorbing the free blocks that follow it, into one list
	// (s0 in the C code; tail == 0 stands for the list head).
	var s0Next, tail uint32
	for i := range a.bList {
		for a.bList[i].next != 0 {
			p := a.removeBlk(uint32(i))
			nu := a.get32(p + blkNU)
			if nu == 0 {
				continue
			}
			for {
				p1 := p + nu*unitSize
				if a.get32(p1+blkStamp) != ^uint32(0) {
					break
				}
				nu += a.get32(p1 + blkNU)
				a.set32(p1+blkNU, 0)
			}
			a.set32(p+blkNU, nu)
			a.set32(p+blkNext, 0)
			if tail == 0 {
				s0Next = p
			} else {
				a.set32(tail+blkNext, p)
			}
			tail = p
		}
	}
	// Second pass: put the merged blocks back on the free lists.
	for s0Next != 0 {
		p := s0Next
		s0Next = a.get32(p + blkNext)
		sz := a.get32(p + blkNU)
		if sz == 0 {
			continue
		}
		for ; sz > 128; sz, p = sz-128, p+128*unitSize {
			a.insertBlk(nIndexes-1, p, 128)
		}
		i := uint32(units2Indx[sz-1])
		if uint32(indx2Units[i]) != sz {
			i--
			k := sz - uint32(indx2Units[i])
			a.insertBlk(k-1, p+(sz-k)*unitSize, k)
		}
		a.insertBlk(i, p, uint32(indx2Units[i]))
	}
	a.glueCount = 1 << 13
}

// allocUnitsRare is the slow path of allocUnits; it returns 0 when the heap is exhausted.
func (a *allocator) allocUnitsRare(indx uint32) uint32 {
	i := indx
	if a.glueCount == 0 {
		a.glueFreeBlocks()
		if a.bList[i].next != 0 {
			return a.removeBlk(i)
		}
	}
	for {
		i++
		if i == nIndexes {
			a.glueCount--
			n := unitSize * uint32(indx2Units[indx])
			if int64(a.unitsStart)-int64(a.pText) > int64(n) {
				a.unitsStart -= n
				return a.unitsStart
			}
			return 0
		}
		if a.bList[i].next != 0 {
			break
		}
	}
	ret := a.removeBlk(i)
	a.splitBlock(ret, i, indx)
	return ret
}

// allocUnits returns a block of nu units (1..128) or 0.
func (a *allocator) allocUnits(nu uint32) uint32 {
	indx := uint32(units2Indx[nu-1])
	if a.bList[indx].next != 0 {
		return a.removeBlk(indx)
	}
	ret := a.loUnit
	a.loUnit += unitSize * uint32(indx2Units[indx])
	if a.loUnit <= a.hiUnit {
		return ret
	}
	a.loUnit -= unitSize * uint32(indx2Units[indx])
	return a.allocUnitsRare(indx)
}

// allocContext returns one unit from the contexts area, falling back to the units area.
func (a *allocator) allocContext() uint32 {
	if a.hiUnit != a.loUnit {
		a.hiUnit -= unitSize
		return a.hiUnit
	}
	if a.bList[0].next != 0 {
		return a.removeBlk(0)
	}
	return a.allocUnitsRare(0)
}

func (a *allocator) unitsCpy(dst, src, nu uint32) {
	n := nu * unitSize
	copy(a.heap[dst:dst+n], a.heap[src:src+n])
}

// expandUnits grows a block of oldNU units by one unit; it returns 0 when out of memory.
func (a *allocator) expandUnits(oldPtr, oldNU uint32) uint32 {
	i0 := uint32(units2Indx[oldNU-1])
	i1 := uint32(units2Indx[oldNU])
	if i0 == i1 {
		return oldPtr
	}
	ptr := a.allocUnits(oldNU + 1)
	if ptr != 0 {
		a.unitsCpy(ptr, oldPtr, oldNU)
		a.insertBlk(i0, oldPtr, oldNU)
	}
	return ptr
}

// shrinkUnits shrinks a block from oldNU to newNU units.
func (a *allocator) shrinkUnits(oldPtr, oldNU, newNU uint32) uint32 {
	i0 := uint32(units2Indx[oldNU-1])
	i1 := uint32(units2Indx[newNU-1])
	if i0 == i1 {
		return oldPtr
	}
	if a.bList[i1].next != 0 {
		ptr := a.removeBlk(i1)
		a.unitsCpy(ptr, oldPtr, newNU)
		a.insertBlk(i0, oldPtr, uint32(indx2Units[i0]))
		return ptr
	}
	a.splitBlock(oldPtr, i0, i1)
	return oldPtr
}

func (a *allocator) freeUnits(ptr, nu uint32) {
	indx := uint32(units2Indx[nu-1])
	a.insertBlk(indx, ptr, uint32(indx2Units[indx]))
}

// specialFreeUnit frees one unit; the unit at unitsStart goes back to the text area instead.
func (a *allocator) specialFreeUnit(ptr uint32) {
	if ptr != a.unitsStart {
		a.insertBlk(0, ptr, 1)
		return
	}
	a.set32(ptr, ^uint32(0))
	a.unitsStart += unitSize
}

// moveUnitsUp moves a block of nu units towards the start of the units area when a free block
// below it is available.
func (a *allocator) moveUnitsUp(oldPtr, nu uint32) uint32 {
	indx := uint32(units2Indx[nu-1])
	if oldPtr > a.unitsStart+16*1024 || oldPtr > a.bList[indx].next {
		return oldPtr
	}
	ptr := a.removeBlk(indx)
	a.unitsCpy(ptr, oldPtr, nu)
	nu = uint32(indx2Units[indx])
	if oldPtr != a.unitsStart {
		a.insertBlk(indx, oldPtr, nu)
	} else {
		a.unitsStart += unitSize * nu
	}
	return ptr
}

// expandTextArea gives the free blocks at the start of the units area to the text area.
func (a *allocator) expandTextArea() {
	var count [nIndexes]uint32
	for a.get32(a.unitsStart+blkStamp) == ^uint32(0) {
		pm := a.unitsStart
		nu := a.get32(pm + blkNU)
		a.unitsStart = pm + nu*unitSize
		count[units2Indx[nu-1]]++
		a.set32(pm+blkStamp, 0)
	}
	for i := range a.bList {
		// prev is the node whose next field is examined; 0 stands for the list head.
		var prev uint32
		for count[i] != 0 {
			next := a.bList[i].next
			if prev != 0 {
				next = a.get32(prev + blkNext)
			}
			for next != 0 && a.get32(next+blkStamp) == 0 {
				// prev.unlink()
				next = a.get32(next + blkNext)
				if prev != 0 {
					a.set32(prev+blkNext, next)
				} else {
					a.bList[i].next = next
				}
				a.bList[i].stamp--
				if count[i]--; count[i] == 0 {
					break
				}
			}
			if next == 0 {
				break
			}
			prev = next
		}
	}
}
