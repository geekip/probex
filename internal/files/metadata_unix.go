//go:build !windows

package files

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"strconv"
	"strings"
	"syscall"
)

func roots() ([]Entry, bool, error)      { return nil, false, nil }
func hidden(d fs.DirEntry) (bool, error) { return strings.HasPrefix(d.Name(), "."), nil }
func metadata(e *Entry, info fs.FileInfo, has func(string) bool) {
	if has("mode") {
		mode := info.Mode().String()
		e.Mode = &mode
	}
	stat := info.Sys().(*syscall.Stat_t)
	if has("owner") {
		uid := uint32(stat.Uid)
		e.UID = &uid
		if owner, err := user.LookupId(strconv.FormatUint(uint64(uid), 10)); err == nil {
			e.Owner = &owner.Username
		}
	}
	if has("group") {
		gid := uint32(stat.Gid)
		e.GID = &gid
		if group, err := user.LookupGroupId(strconv.FormatUint(uint64(gid), 10)); err == nil {
			e.Group = &group.Name
		}
	}
}

func preserveOwner(file *os.File, original fs.FileInfo) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	owner := original.Sys().(*syscall.Stat_t)
	current := info.Sys().(*syscall.Stat_t)
	if owner.Uid == current.Uid && owner.Gid == current.Gid {
		return nil
	}
	if err := file.Chown(int(owner.Uid), int(owner.Gid)); err != nil {
		return fmt.Errorf("preserve ownership: %w", err)
	}
	return nil
}

func spaceError(err error) bool { return errors.Is(err, syscall.ENOSPC) }
