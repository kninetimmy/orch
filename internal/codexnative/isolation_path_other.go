//go:build !windows

package codexnative

import "os"

func holdInstructionFile(string) (*os.File, error) { return nil, ErrIsolationUnavailable }

func finalIsolationPath(path string) (string, error) { return path, nil }
