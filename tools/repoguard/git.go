// Package repoguard enforces publication and immutable baseline rules. It never
// loads private configuration or prints scanned values in diagnostics.
package repoguard

import (
	"fmt"
	"os/exec"
	"path/filepath"
)

func Git(root string, args ...string) ([]byte, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("git", append([]string{"-c", "safe.directory=" + filepath.ToSlash(abs), "-C", abs}, args...)...)
	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("Git operation failed: %s", args[0])
	}
	return data, nil
}
