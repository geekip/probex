package files

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows"
)

func isShortcut(path string) bool { return strings.EqualFold(filepath.Ext(path), ".lnk") }

func shortcutFile(path string) (bool, error) {
	if !isShortcut(path) {
		return false, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	var header [20]byte
	_, err = io.ReadFull(f, header[:])
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return false, err
	}
	// MS-SHLLINK: HeaderSize 0x4c followed by the Shell Link CLSID.
	return bytes.Equal(header[:], []byte{0x4c, 0, 0, 0, 1, 0x14, 2, 0, 0, 0, 0, 0, 0xc0, 0, 0, 0, 0, 0, 0, 0x46}), nil
}

// MoveFileEx cannot replace a directory symlink with a file symlink or vice versa.
// Move the old alias aside first and restore it if installing the new one fails.
func replaceLink(temp, destination string, replace bool) error {
	if !replace {
		return os.Rename(temp, destination)
	}
	previous := temp + ".previous"
	if err := os.Rename(destination, previous); err != nil {
		return err
	}
	if err := os.Rename(temp, destination); err != nil {
		return errors.Join(err, os.Rename(previous, destination))
	}
	return os.Remove(previous)
}

// COM objects and their releases must stay in the initialized apartment.
func withShortcut(path string, action func(*ole.IDispatch) (string, error)) (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		return "", err
	}
	defer ole.CoUninitialize()
	object, err := oleutil.CreateObject("WScript.Shell")
	if err != nil {
		return "", err
	}
	defer object.Release()
	shell, err := object.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return "", err
	}
	defer shell.Release()
	value, err := oleutil.CallMethod(shell, "CreateShortcut", path)
	if err != nil {
		return "", err
	}
	defer value.Clear()
	return action(value.ToIDispatch())
}

func shortcutTarget(path string) (string, error) {
	return withShortcut(path, func(shortcut *ole.IDispatch) (string, error) {
		value, err := oleutil.GetProperty(shortcut, "TargetPath")
		if err != nil {
			return "", err
		}
		defer value.Clear()
		target := value.ToString()
		if target == "" {
			return "", fmt.Errorf("shortcut has no filesystem target: %w", os.ErrInvalid)
		}
		return target, nil
	})
}

func writeShortcut(path, target string) error {
	_, err := withShortcut(path, func(shortcut *ole.IDispatch) (string, error) {
		value, err := oleutil.PutProperty(shortcut, "TargetPath", target)
		if err != nil {
			return "", err
		}
		value.Clear()
		value, err = oleutil.CallMethod(shortcut, "Save")
		if err != nil {
			return "", err
		}
		value.Clear()
		return "", nil
	})
	return err
}

// Directory metadata omits the link count. Read it from a handle when requested.
func hardLinksFromInfo(os.FileInfo) uint64 { return 0 }

func hardLinks(path string, _ os.FileInfo) (uint64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	handle, err := windows.CreateFile(p, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(handle)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return 0, err
	}
	return uint64(info.NumberOfLinks), nil
}
