//go:build !linux

package files

import "os"

func preserveAccessACL(_ *os.File, _ string) error { return nil }
