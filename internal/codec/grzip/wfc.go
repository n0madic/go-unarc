package grzip

// wfcList is the recency list of the WFC coder: characters ordered by a weight that rises when a character is
// seen and decays over the next twelve distinct runs (WFC_Ari.c). The list has a sentinel entry (256) of weight
// -1, below every real weight, which stops the re-sorting loop.
type wfcList struct {
	// The arrays have 257 used entries (256 characters and the sentinel) but are sized 512 so that masking
	// the index with wfcMask proves the accesses in range to the compiler.
	list   [512]int // characters by descending weight
	index  [512]int // position of each character in list
	weight [512]int32
	hist   [wfcHistory]byte // the characters of the last runs, indexed by run number modulo wfcHistory
}

const (
	wfcSentinel = 256
	wfcMask     = 511
	wfcHistory  = 4096 // a power of two above the largest lookback (2048): the slot of run n-2048 is still intact when run n is stored
	wfcVal0     = 131072
)

// wfcSteps are the weights taken from the character seen 1, 2, 4, 8, ... 2048 runs ago. With wfcVal0 they sum to
// zero, so a weight is never negative, whatever the data.
var wfcSteps = [...]struct {
	val int32
	pos int
}{
	{114688, 1}, {7272, 2}, {4240, 4}, {2364, 8}, {1263, 16}, {649, 32},
	{320, 64}, {153, 128}, {70, 256}, {31, 512}, {14, 1024}, {8, 2048},
}

// init resets the list.
func (w *wfcList) init() {
	for i := range w.list {
		w.list[i] = i
		w.index[i] = i
		w.weight[i] = 0
	}
	w.weight[wfcSentinel] = -1
}

// pick returns the character at list position pos for run number n and updates the weights: the picked
// character gains the full weight and moves to the front; the characters seen 1, 2, 4, ... runs ago lose a
// step each and move back past every character that is now heavier.
func (w *wfcList) pick(pos, n int) byte {
	c := byte(w.list[pos&wfcMask])
	w.hist[uint(n)&(wfcHistory-1)] = c
	w.raise(c)
	for i := range wfcSteps {
		s := &wfcSteps[i]
		if n < s.pos {
			break // the steps are ordered by distance
		}
		old := w.hist[uint(n-s.pos)&(wfcHistory-1)]
		wt := w.weight[old] - s.val
		w.weight[old] = wt
		if old == c {
			continue // stays in front
		}
		j := w.index[old]
		for {
			next := w.list[(j+1)&wfcMask]
			if w.weight[next&wfcMask] <= wt {
				break
			}
			w.list[j&wfcMask] = next
			w.index[next&wfcMask] = j
			j++
		}
		w.list[j&wfcMask] = int(old)
		w.index[old] = j
	}
	return c
}

// raise adds the full weight to c and moves it to the front.
func (w *wfcList) raise(c byte) {
	w.weight[c] += wfcVal0
	j := w.index[c]
	for ; j > 0; j-- {
		prev := w.list[(j-1)&wfcMask]
		w.list[j&wfcMask] = prev
		w.index[prev&wfcMask] = j
	}
	w.list[0] = int(c)
	w.index[c] = 0
}
