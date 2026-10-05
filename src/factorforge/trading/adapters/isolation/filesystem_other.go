//go:build !linux

package isolation

func RestrictFilesystem(readPaths, writablePaths []string) error {
	return fail("LINUX_ISOLATION_REQUIRED")
}
