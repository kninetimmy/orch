//go:build !windows

package codexnative

func finalIsolationPath(path string) (string, error) { return path, nil }
