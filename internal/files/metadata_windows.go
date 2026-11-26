package files

import (
	"errors"
	"golang.org/x/sys/windows"
	"io/fs"
	"os"
	"syscall"
)

func roots() ([]Entry, bool, error) {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil, true, err
	}
	list := []Entry{}
	for i := 0; i < 26; i++ {
		if mask&(1<<i) != 0 {
			path := string(rune('A'+i)) + ":\\"
			list = append(list, Entry{Name: path, Path: path, Type: "directory"})
		}
	}
	return list, true, nil
}
func hidden(d fs.DirEntry) (bool, error) {
	info, err := d.Info()
	if err != nil {
		return false, err
	}
	return info.Sys().(*syscall.Win32FileAttributeData).FileAttributes&windows.FILE_ATTRIBUTE_HIDDEN != 0, nil
}
func metadata(e *Entry, info fs.FileInfo, has func(string) bool) {}

func preserveOwner(*os.File, fs.FileInfo) error { return nil }

func spaceError(err error) bool {
	return errors.Is(err, windows.ERROR_DISK_FULL) || errors.Is(err, windows.ERROR_HANDLE_DISK_FULL)
}
