//go:build !windows

package paths

func verifyCanonicalPath(string) error { return nil }
