//go:build !windows

package nativehost

import "os"

func HoldInstructionFile(string) (*os.File, error) { return nil, ErrHoldUnsupported }

func finalIsolationPath(path string) (string, error) { return path, nil }
