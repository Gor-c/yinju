package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// rotateWriter appends to <dir>/<name>.log and rotates the file to
// <name>-YYYYMMDD-HHMMSS.mmm.log once it exceeds maxBytes. Old rotated files
// are pruned to maxBackups.
type rotateWriter struct {
	mu         sync.Mutex
	dir        string
	name       string
	maxBytes   int64
	maxBackups int

	file   *os.File
	size   int64
	closed bool
}

func newRotateWriter(dir, name string, maxSizeMB, maxBackups int) (*rotateWriter, error) {
	w := &rotateWriter{
		dir:        dir,
		name:       name,
		maxBytes:   int64(maxSizeMB) << 20,
		maxBackups: maxBackups,
	}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rotateWriter) path() string {
	return filepath.Join(w.dir, w.name+".log")
}

func (w *rotateWriter) open() error {
	file, err := os.OpenFile(w.path(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
	if err != nil {
		return fmt.Errorf("open log file %s: %w", w.path(), err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return fmt.Errorf("stat log file %s: %w", w.path(), err)
	}
	w.file = file
	w.size = info.Size()
	return nil
}

func (w *rotateWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, fmt.Errorf("log file %s is closed", w.path())
	}
	if w.maxBytes > 0 && w.size > 0 && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotateLocked(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotateWriter) rotateLocked() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	stamp := time.Now().Format("20060102-150405.000")
	target := filepath.Join(w.dir, fmt.Sprintf("%s-%s.log", w.name, stamp))
	if err := os.Rename(w.path(), target); err != nil {
		// Reopen the original file so logging can continue on the next write.
		reopenErr := w.open()
		if reopenErr != nil {
			return fmt.Errorf("rotate log file %s: %w (reopen: %v)", w.path(), err, reopenErr)
		}
		return fmt.Errorf("rotate log file %s: %w", w.path(), err)
	}
	if err := w.open(); err != nil {
		return err
	}
	w.pruneLocked()
	return nil
}

func (w *rotateWriter) pruneLocked() {
	if w.maxBackups < 0 {
		return
	}
	matches, err := filepath.Glob(filepath.Join(w.dir, w.name+"-*.log"))
	if err != nil {
		return
	}
	sort.Strings(matches)
	for len(matches) > w.maxBackups {
		_ = os.Remove(matches[0])
		matches = matches[1:]
	}
}

func (w *rotateWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	if w.file == nil {
		return nil
	}
	return w.file.Close()
}
