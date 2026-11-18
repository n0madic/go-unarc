package extract

import (
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func setMtime(f *os.File, seconds uint32) {
	mtime := windows.NsecToFiletime(time.Unix(int64(seconds), 0).UnixNano())
	_ = windows.SetFileTime(windows.Handle(f.Fd()), nil, nil, &mtime)
}
