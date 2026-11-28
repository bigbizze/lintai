package workspace

import (
	"bytes"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// onDiskSpelling asks the kernel for the path of the open file, which names
// each directory entry as it is stored on disk.
func onDiskSpelling(path string) (string, error) {
	fd, err := unix.Open(path, unix.O_EVTONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", fmt.Errorf("resolve the on-disk spelling of %s: %w", path, err)
	}
	defer unix.Close(fd)
	var buf [unix.PathMax]byte
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), unix.F_GETPATH, uintptr(unsafe.Pointer(&buf[0]))); errno != 0 {
		return "", fmt.Errorf("resolve the on-disk spelling of %s: %w", path, errno)
	}
	end := bytes.IndexByte(buf[:], 0)
	if end <= 0 {
		return "", fmt.Errorf("resolve the on-disk spelling of %s: empty path", path)
	}
	return string(buf[:end]), nil
}
