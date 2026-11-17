// Package cleanup keeps the list of files to remove if the program is interrupted
// (temp files of chain stages, the output file being written).
package cleanup

import (
	"os"
	"sync"
)

var (
	mu    sync.Mutex
	next  int
	paths = map[int]string{}
)

// Register remembers path for removal on interrupt and returns the function that forgets it again.
func Register(path string) (forget func()) {
	mu.Lock()
	defer mu.Unlock()
	id := next
	next++
	paths[id] = path
	return func() {
		mu.Lock()
		defer mu.Unlock()
		delete(paths, id)
	}
}

// RemoveAll removes every registered path, newest first so that files go before their folders.
func RemoveAll() {
	mu.Lock()
	defer mu.Unlock()
	for id := next - 1; id >= 0; id-- {
		if p, ok := paths[id]; ok {
			_ = os.RemoveAll(p) // best effort: the program is exiting
			delete(paths, id)
		}
	}
}
