package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type manifest struct {
	Version string            `json:"version"`
	At      time.Time         `json:"at"`
	Files   map[string]string `json:"files"`
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fail("BACKUP_SYMLINK_REJECTED")
		}
		rel, e := filepath.Rel(src, path)
		if e != nil {
			return e
		}
		to := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(to, 0700)
		}
		if !d.Type().IsRegular() {
			return fail("BACKUP_SPECIAL_FILE_REJECTED")
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		return write(to, b)
	})
}
func hashFile(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	_, e = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), e
}
func outputFile(c *exec.Cmd, path string) error {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	c.Stdout = f
	c.Stderr = io.Discard
	if c.Run() != nil {
		return fail("DATABASE_BACKUP_FAILED")
	}
	return f.Sync()
}
func (d *deployment) backup() error {
	if e := d.stopped(); e != nil {
		return e
	}
	if e := d.compose("up", "-d", "--wait", "postgres"); e != nil {
		return e
	}
	now := time.Now().UTC()
	dir := filepath.Join(d.o.DataRoot, "backups", now.Format("2006-01-02"), now.Format("150405.000000000"))
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	if e := outputFile(d.command("exec", "-T", "postgres", "pg_dump", "-U", "ff_admin", "-Fc", "factorforge"), filepath.Join(dir, "database.dump")); e != nil {
		return e
	}
	if e := outputFile(d.command("exec", "-T", "postgres", "pg_dumpall", "-U", "ff_admin", "--globals-only"), filepath.Join(dir, "roles.sql")); e != nil {
		return e
	}
	b, e := os.ReadFile(d.config)
	if e != nil {
		return e
	}
	if e = write(filepath.Join(dir, "config/config.toml"), b); e != nil {
		return e
	}
	for _, name := range []string{"assets", "tls", "model-runtime"} {
		if e = copyTree(filepath.Join(d.o.DataRoot, name), filepath.Join(dir, name)); e != nil {
			return e
		}
	}
	if e = copyTree(filepath.Join(d.root, "prompts"), filepath.Join(dir, "prompts")); e != nil {
		return e
	}
	m := manifest{d.o.ImageTag, now, map[string]string{}}
	if e = filepath.WalkDir(dir, func(path string, de os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if de.IsDir() {
			return nil
		}
		rel, e := filepath.Rel(dir, path)
		if e != nil {
			return e
		}
		h, e := hashFile(path)
		m.Files[filepath.ToSlash(rel)] = h
		return e
	}); e != nil {
		return e
	}
	b, e = json.MarshalIndent(m, "", "  ")
	if e != nil {
		return e
	}
	if e = write(filepath.Join(dir, "COMPLETE.json"), b); e != nil {
		return e
	}
	fmt.Println("Backup complete:", dir)
	return nil
}
func checkedBackup(source string) (manifest, error) {
	var m manifest
	b, e := os.ReadFile(filepath.Join(source, "COMPLETE.json"))
	if e != nil || json.Unmarshal(b, &m) != nil || len(m.Files) == 0 {
		return m, fail("BACKUP_INCOMPLETE")
	}
	for rel, want := range m.Files {
		clean := filepath.Clean(filepath.FromSlash(rel))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return m, fail("BACKUP_PATH_REJECTED")
		}
		p := filepath.Join(source, clean)
		if e = privatePath(p); e != nil {
			return m, e
		}
		h, e := hashFile(p)
		if e != nil || h != want {
			return m, fail("BACKUP_CHECKSUM_FAILED")
		}
	}
	for _, name := range []string{"database.dump", "roles.sql", "config/config.toml"} {
		if m.Files[name] == "" {
			return m, fail("BACKUP_REQUIRED_FILE_MISSING")
		}
	}
	return m, nil
}
func (d *deployment) restore(source, dest string) error {
	if !filepath.IsAbs(source) || !filepath.IsAbs(dest) || dest == "/" {
		return fail("ABSOLUTE_RESTORE_PATHS_REQUIRED")
	}
	if e := privatePath(source); e != nil {
		return e
	}
	if e := privatePath(dest); e != nil {
		return e
	}
	if _, e := os.Lstat(dest); !os.IsNotExist(e) {
		return fail("RESTORE_DESTINATION_MUST_NOT_EXIST")
	}
	m, e := checkedBackup(source)
	if e != nil {
		return e
	}
	if e = os.Mkdir(dest, 0700); e != nil {
		return e
	}
	// Copy only manifest-listed, checked assets; logs stay in the separate archive.
	for rel := range m.Files {
		if rel == "database.dump" || rel == "roles.sql" {
			continue
		}
		b, e := os.ReadFile(filepath.Join(source, rel))
		if e != nil {
			return e
		}
		if e = write(filepath.Join(dest, rel), b); e != nil {
			return e
		}
	}
	for _, name := range []string{"postgres", "logs", "bootstrap"} {
		if e = os.MkdirAll(filepath.Join(dest, name), 0700); e != nil {
			return e
		}
	}
	pass := filepath.Join(dest, "bootstrap/password")
	if e = write(pass, []byte(secret())); e != nil {
		return e
	}
	name := "ff-restore-" + time.Now().UTC().Format("20060102t150405") + "-" + fmt.Sprint(os.Getpid())
	args := []string{"run", "-d", "--name", name, "--network", "none", "--init", "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "--memory", "512m", "--cpus", "1", "--pids-limit", "128", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--tmpfs", "/tmp:rw,size=64m", "--tmpfs", "/var/run/postgresql:rw,size=16m", "-e", "POSTGRES_USER=ff_admin", "-e", "POSTGRES_PASSWORD_FILE=/bootstrap/password", "-e", "PGDATA=/var/lib/postgresql/data/pgdata", "--mount", "type=bind,src=" + filepath.Join(dest, "postgres") + ",dst=/var/lib/postgresql/data", "--mount", "type=bind,src=" + filepath.Join(dest, "logs") + ",dst=/logs", "--mount", "type=bind,src=" + filepath.Join(dest, "bootstrap") + ",dst=/bootstrap,readonly", "factorforge-postgres:" + d.o.ImageTag, "postgres", "-c", "log_statement=none", "-c", "log_min_error_statement=panic"}
	if exec.Command("docker", args...).Run() != nil {
		return fail("RESTORE_DATABASE_START_FAILED")
	}
	defer func() {
		_ = exec.Command("docker", "stop", "-t", "30", name).Run()
		_ = exec.Command("docker", "rm", name).Run()
	}()
	ready := false
	for i := 0; i < 60; i++ {
		// initdb's temporary server accepts Unix sockets while bootstrap is still
		// running. Only the final server listens on TCP.
		if exec.Command("docker", "exec", name, "pg_isready", "-h", "127.0.0.1", "-U", "ff_admin", "-d", "postgres").Run() == nil {
			ready = true
			break
		}
		time.Sleep(time.Second)
	}
	if !ready {
		return fail("RESTORE_DATABASE_NOT_READY")
	}
	runFile := func(file string, args ...string) error {
		f, e := os.Open(filepath.Join(source, file))
		if e != nil {
			return e
		}
		defer f.Close()
		c := exec.Command("docker", append([]string{"exec", "-i", name}, args...)...)
		c.Stdin = f
		if file == "roles.sql" {
			b, err := io.ReadAll(f)
			if err != nil {
				return err
			}
			const bootstrap = "\nCREATE ROLE ff_admin;\n"
			if strings.Count(string(b), bootstrap) != 1 {
				return fail("RESTORE_BOOTSTRAP_ROLE_MISMATCH")
			}
			c.Stdin = strings.NewReader(strings.Replace(string(b), bootstrap, "\n-- bootstrap role already exists\n", 1))
		}
		// Diagnostics can contain SQL or private values. Keep them in the private
		// restore directory, never relay them to the terminal or application logs.
		diagnostic, e := os.OpenFile(filepath.Join(dest, "bootstrap", file+".stderr"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		defer diagnostic.Close()
		c.Stderr = diagnostic
		if c.Run() != nil {
			return fmt.Errorf("DATABASE_RESTORE_FAILED stage=%s; private diagnostics in restore bootstrap directory", file)
		}
		return nil
	}
	if e = runFile("roles.sql", "psql", "-X", "-U", "ff_admin", "-d", "postgres", "-v", "ON_ERROR_STOP=1"); e != nil {
		return e
	}
	if exec.Command("docker", "exec", name, "createdb", "-U", "ff_admin", "-O", "ff_admin", "factorforge").Run() != nil {
		return fail("RESTORE_CREATE_DATABASE_FAILED")
	}
	if e = runFile("database.dump", "pg_restore", "-U", "ff_admin", "--exit-on-error", "-d", "factorforge"); e != nil {
		return e
	}
	// Verify every schema restored and readable; business reconciliation is separate.
	c := exec.Command("docker", "exec", name, "psql", "-U", "ff_admin", "-d", "factorforge", "-Atc", "SELECT count(*) FROM pg_catalog.pg_class WHERE relkind='r'")
	if _, e = c.Output(); e != nil {
		return fail("RESTORE_VERIFICATION_FAILED")
	}
	b, _ := json.Marshal(map[string]any{"source_version": m.Version, "restored_at": time.Now().UTC(), "business_reconciled": false})
	if e = write(filepath.Join(dest, "RESTORED.json"), b); e != nil {
		return e
	}
	fmt.Println("Restored to isolated directory; not activated:", dest)
	return nil
}
