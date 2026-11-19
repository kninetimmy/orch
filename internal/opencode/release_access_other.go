//go:build !windows

package opencode

import "os"

func releaseDeleteAccess(string, os.FileInfo) error { return nil }
