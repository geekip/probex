package files

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func preserveAccessACL(file *os.File, path string) error {
	const attribute = "system.posix_acl_access"
	size, err := unix.Getxattr(path, attribute, nil)
	switch {
	case errors.Is(err, unix.ENOTSUP):
		return nil
	case errors.Is(err, unix.ENODATA):
		// A newly created file may have inherited the directory's default ACL.
		err = unix.Fremovexattr(int(file.Fd()), attribute)
		if errors.Is(err, unix.ENODATA) || errors.Is(err, unix.ENOTSUP) {
			return nil
		}
	case err == nil:
		acl := make([]byte, size)
		n, readErr := unix.Getxattr(path, attribute, acl)
		if readErr != nil {
			err = readErr
		} else {
			err = unix.Fsetxattr(int(file.Fd()), attribute, acl[:n], 0)
		}
	}
	if err != nil {
		return fmt.Errorf("preserve access ACL: %w", err)
	}
	return nil
}
