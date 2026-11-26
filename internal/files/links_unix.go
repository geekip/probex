//go:build !windows

package files

import (
	"os"
	"syscall"
)

func replaceLink(temp, destination string, _ bool) error { return os.Rename(temp, destination) }

func isShortcut(string) bool                { return false }
func shortcutFile(string) (bool, error)     { return false, nil }
func shortcutTarget(string) (string, error) { return "", os.ErrInvalid }
func writeShortcut(string, string) error    { return os.ErrInvalid }
func hardLinks(_ string, info os.FileInfo) (uint64, error) {
	return uint64(info.Sys().(*syscall.Stat_t).Nlink), nil
}
