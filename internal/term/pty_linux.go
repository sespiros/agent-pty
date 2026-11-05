package term

import (
	"os"
	"strconv"
	"syscall"
	"unsafe"
)

const (
	reqGet = syscall.TCGETS
	reqSet = syscall.TCSETS
)

func unlock(m *os.File) (string, error) {
	fd := m.Fd()
	var lock int32
	if err := ioctl(fd, syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&lock))); err != nil {
		return "", err
	}
	var n uint32
	if err := ioctl(fd, syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); err != nil {
		return "", err
	}
	return "/dev/pts/" + strconv.FormatUint(uint64(n), 10), nil
}
