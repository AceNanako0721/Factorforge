//go:build linux

package isolation

import (
	"github.com/landlock-lsm/go-landlock/landlock"
	"os"
)

// RestrictFilesystem is strict: unsupported kernels fail, without BestEffort.
// Read permissions name exact profiles rather than the private config folder.
func RestrictFilesystem(readPaths, writablePaths []string) error {
	rules := []landlock.Rule{}
	for _, path := range readPaths {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fail("LANDLOCK_RULE_FAILED")
		}
		if info.IsDir() {
			rules = append(rules, landlock.RODirs(path))
		} else {
			rules = append(rules, landlock.ROFiles(path))
		}
	}
	for _, path := range writablePaths {
		info, err := os.Stat(path)
		if err != nil {
			return fail("LANDLOCK_RULE_FAILED")
		}
		if info.IsDir() {
			rules = append(rules, landlock.RWDirs(path))
		} else {
			rules = append(rules, landlock.RWFiles(path))
		}
	}
	if err := landlock.V3.RestrictPaths(rules...); err != nil {
		return fail("LANDLOCK_RESTRICTION_FAILED")
	}
	return nil
}
