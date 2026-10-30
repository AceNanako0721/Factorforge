package deployment_test

import (
	"bytes"
	"compress/gzip"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/applications/deployment/adapters"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func contents(t *testing.T, root string) []byte {
	t.Helper()
	var all []byte
	e := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !strings.HasSuffix(p, ".gz") {
			return nil
		}
		f, e := os.Open(p)
		if e != nil {
			return e
		}
		defer f.Close()
		z, e := gzip.NewReader(f)
		if e != nil {
			return e
		}
		b, e := io.ReadAll(z)
		if e != nil {
			return e
		}
		if e = z.Close(); e != nil {
			return e
		}
		all = append(all, b...)
		s, _ := os.Stat(p)
		if s.Mode().Perm() != 0600 {
			t.Errorf("archive mode %o", s.Mode().Perm())
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return all
}
func TestArchiveCrossYearRotationPreservesEveryByte(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2024, 2, 29, 23, 59, 59, 0, time.UTC)
	clock := func() time.Time { return now }
	sink, e := a.OpenArchive(root, 17, clock)
	if e != nil {
		t.Fatal(e)
	}
	first := bytes.Repeat([]byte("old-before-upgrade\n"), 20)
	if _, e = sink.Write(first); e != nil {
		t.Fatal(e)
	}
	now = now.AddDate(1, 0, 1)
	second := bytes.Repeat([]byte("new-after-upgrade\n"), 20)
	if _, e = sink.Write(second); e != nil {
		t.Fatal(e)
	}
	if e = sink.Close(); e != nil {
		t.Fatal(e)
	}
	b := contents(t, root)
	if !bytes.Equal(b, append(first, second...)) {
		t.Fatal("rotation or year change lost/reordered data")
	}
	// New process and clock rollback must not overwrite existing history.
	now = time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	sink, e = a.OpenArchive(root, 17, clock)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = sink.Write([]byte("clock-backwards\n")); e != nil {
		t.Fatal(e)
	}
	if e = sink.Close(); e != nil {
		t.Fatal(e)
	}
	b = contents(t, root)
	if !bytes.Contains(b, first) || !bytes.Contains(b, second) || !bytes.Contains(b, []byte("clock-backwards")) {
		t.Fatal("clock rollback deleted history")
	}
}
func TestArchiveExclusiveWriterAndCrashTailRecovery(t *testing.T) {
	root := t.TempDir()
	day := filepath.Join(root, "2020-01-01")
	if e := os.Mkdir(day, 0700); e != nil {
		t.Fatal(e)
	}
	tail := []byte("complete lines and partial-tail")
	if e := os.WriteFile(filepath.Join(day, "crash.open"), tail, 0600); e != nil {
		t.Fatal(e)
	}
	s, e := a.OpenArchive(root, 32, time.Now)
	if e != nil {
		t.Fatal(e)
	}
	if other, e := a.OpenArchive(root, 32, time.Now); e == nil {
		other.Close()
		t.Fatal("second writer admitted")
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(contents(t, root), tail) {
		t.Fatal("crash tail lost")
	}
}
func TestArchiveRefusesSymlinkAndInvalidPolicy(t *testing.T) {
	root := t.TempDir()
	if _, e := a.OpenArchive(root, 0, time.Now); e == nil {
		t.Fatal("invalid policy accepted")
	}
	if e := os.Symlink(t.TempDir(), filepath.Join(root, "escape")); e != nil {
		t.Fatal(e)
	}
	if s, e := a.OpenArchive(root, 16, time.Now); e == nil {
		s.Close()
		t.Fatal("symlink accepted")
	}
}
