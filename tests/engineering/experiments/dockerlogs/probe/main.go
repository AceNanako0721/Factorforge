// Experimental log supervisor and synthetic child. Never used by production.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

const maxBytes int64 = 16 * 1024 // Small, synthetic rotation budget for the lab.
const backups = 3

type sink struct {
	mu         sync.Mutex
	file, lock *os.File
	path       string
	size       int64
}

func openSink(dir string) (*sink, error) {
	s := &sink{path: filepath.Join(dir, "service.log")}
	var err error
	s.lock, err = os.OpenFile(filepath.Join(dir, ".writer.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(s.lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		s.lock.Close()
		return nil, err
	}
	if err = s.open(); err != nil {
		s.lock.Close()
		return nil, err
	}
	return s, nil
}

func (s *sink) open() error {
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	s.file, s.size = f, info.Size()
	return nil
}

func (s *sink) rotate() error {
	if err := s.file.Close(); err != nil {
		return err
	}
	if err := os.Remove(s.path + "." + strconv.Itoa(backups)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for i := backups - 1; i >= 1; i-- {
		if err := os.Rename(s.path+"."+strconv.Itoa(i), s.path+"."+strconv.Itoa(i+1)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(s.path, s.path+".1"); err != nil {
		return err
	}
	return s.open()
}

func (s *sink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	written := 0
	for written < len(p) {
		if s.size >= maxBytes {
			if err := s.rotate(); err != nil {
				return written, err
			}
		}
		n := min(len(p)-written, int(maxBytes-s.size))
		m, err := s.file.Write(p[written : written+n])
		written += m
		s.size += int64(m)
		if err != nil {
			return written, err
		}
		if m != n {
			return written, io.ErrShortWrite
		}
		if err := s.file.Sync(); err != nil {
			return written, err
		}
	}
	// The durable copy is written before the Docker diagnostic stream.
	return os.Stdout.Write(p)
}

func emit() int {
	tag := os.Getenv("FF_LAB_TAG")
	if tag == "EXIT" {
		fmt.Println("EXIT_MARKER", tag)
		return 37
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	fmt.Println("BOOT", tag)
	fmt.Fprintln(os.Stderr, "STDERR", tag)
	if tag == "ROTATE" {
		for i := 0; i < 300; i++ {
			fmt.Printf("FILL %04d %s\n", i, strings.Repeat("x", 1024))
		}
		fmt.Println("FLOOD_END", tag)
	}
	<-signals
	fmt.Println("TERM", tag)
	return 0
}

func supervise() int {
	s, err := openSink("/logs")
	if err != nil {
		fmt.Fprintln(os.Stderr, "LAB_LOG_OPEN_FAILED")
		return 73
	}
	defer s.lock.Close()
	defer func() { s.file.Close() }()
	cmd := exec.Command("/probe", "emit")
	cmd.Stdout, cmd.Stderr = s, s
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	if err = cmd.Start(); err != nil {
		return 74
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case sig := <-signals:
				_ = syscall.Kill(-cmd.Process.Pid, sig.(syscall.Signal))
			case <-done:
				return
			}
		}
	}()
	if err = cmd.Wait(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() >= 0 {
			return exit.ExitCode()
		}
		return 74
	}
	return 0
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "emit" {
		os.Exit(emit())
	}
	os.Exit(supervise())
}
