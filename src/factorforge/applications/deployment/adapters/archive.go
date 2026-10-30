// Package adapters implements deployment infrastructure, with no business imports.
package adapters

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Archive never deletes history by age or count. Only a verified, durable gzip
// representation may replace a closed plaintext segment.
type Archive struct {
	mu                    sync.Mutex
	root, day, path       string
	file, lock            *os.File
	sequence, size, limit int64
	now                   func() time.Time
}

func OpenArchive(root string, limit int64, now func() time.Time) (*Archive, error) {
	if limit <= 0 || now == nil {
		return nil, errors.New("LOG_POLICY_INVALID")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	// The service-specific mount is private; reject symbolic links throughout it.
	if err := filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.Type()&os.ModeSymlink != 0 {
			return errors.New("LOG_SYMLINK_REJECTED")
		}
		return nil
	}); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(root, ".writer.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, err
	}
	a := &Archive{root: root, limit: limit, now: now, lock: lock}
	// A prior process has exited (the kernel lock proves exclusive ownership).
	paths, err := filepath.Glob(filepath.Join(root, "*", "*.open"))
	if err == nil {
		for _, p := range paths {
			if err = seal(p); err != nil {
				break
			}
		}
	}
	if err == nil {
		err = a.open()
	}
	if err != nil {
		lock.Close()
		return nil, err
	}
	return a, nil
}
func syncDir(path string) error {
	d, e := os.Open(path)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func (a *Archive) open() error {
	a.day = a.now().UTC().Format("2006-01-02")
	dir := filepath.Join(a.root, a.day)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	a.sequence++
	f, e := os.CreateTemp(dir, fmt.Sprintf("%s-%020d-*.open", a.now().UTC().Format("150405.000000000"), a.sequence))
	if e != nil {
		return e
	}
	a.file, a.path, a.size = f, f.Name(), 0
	return syncDir(dir)
}
func seal(path string) error {
	in, e := os.Open(path)
	if e != nil {
		return e
	}
	defer in.Close()
	tmp, e := os.CreateTemp(filepath.Dir(path), ".compress-*")
	if e != nil {
		return e
	}
	defer func() { tmp.Close(); os.Remove(tmp.Name()) }()
	gz := gzip.NewWriter(tmp)
	if _, e = io.Copy(gz, in); e != nil {
		return e
	}
	if e = gz.Close(); e != nil {
		return e
	}
	if e = tmp.Sync(); e != nil {
		return e
	}
	// Verify CRC and uncompressed byte count before replacing original bytes.
	if _, e = tmp.Seek(0, 0); e != nil {
		return e
	}
	zr, e := gzip.NewReader(tmp)
	if e != nil {
		return e
	}
	n, e := io.Copy(io.Discard, zr)
	ce := zr.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	info, e := in.Stat()
	if e != nil {
		return e
	}
	if n != info.Size() {
		return io.ErrUnexpectedEOF
	}
	if e = tmp.Close(); e != nil {
		return e
	}
	target := path + ".gz"
	// Never replace an existing archive, even if a previous crash left both files.
	if _, e = os.Lstat(target); e == nil {
		target = tmp.Name() + ".gz"
	} else if !os.IsNotExist(e) {
		return e
	}
	if e = os.Rename(tmp.Name(), target); e != nil {
		return e
	}
	if e = syncDir(filepath.Dir(path)); e != nil {
		return e
	}
	if e = os.Remove(path); e != nil {
		return e
	}
	return syncDir(filepath.Dir(path))
}
func (a *Archive) rotate() error {
	if a.file != nil {
		if e := a.file.Sync(); e != nil {
			return e
		}
		if e := a.file.Close(); e != nil {
			return e
		}
		a.file = nil
		if e := seal(a.path); e != nil {
			return e
		}
	}
	return a.open()
}
func (a *Archive) Write(p []byte) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for len(p) > 0 {
		if a.file == nil || a.day != a.now().UTC().Format("2006-01-02") || a.size >= a.limit {
			if e := a.rotate(); e != nil {
				return n, e
			}
		}
		take := min(int64(len(p)), a.limit-a.size)
		m, e := a.file.Write(p[:take])
		a.size += int64(m)
		n += m
		p = p[m:]
		if e != nil {
			return n, e
		}
		if m != int(take) {
			return n, io.ErrShortWrite
		}
		if e = a.file.Sync(); e != nil {
			return n, e
		}
	}
	return n, nil
}
func (a *Archive) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	defer a.lock.Close()
	if a.file == nil {
		return nil
	}
	if e := a.file.Sync(); e != nil {
		a.file.Close()
		return e
	}
	if e := a.file.Close(); e != nil {
		return e
	}
	a.file = nil
	return seal(a.path)
}
func ArchiveSize(root string) (int64, error) {
	var n int64
	e := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !d.IsDir() {
			s, e := d.Info()
			if e != nil {
				return e
			}
			n += s.Size()
		}
		return nil
	})
	return n, e
}
func StableError(code string) error { return fmt.Errorf("%s", code) }
