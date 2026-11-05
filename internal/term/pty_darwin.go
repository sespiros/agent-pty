package term

import (
	"bytes"
	"os"
	"syscall"
	"unsafe"
)

const (
	reqGet = syscall.TIOCGETA
	reqSet = syscall.TIOCSETA
)

func unlock(m *os.File) (string, error) {
	fd := m.Fd()
	if err := ioctl(fd, syscall.TIOCPTYGRANT, 0); err != nil {
		return "", err
	}
	if err := ioctl(fd, syscall.TIOCPTYUNLK, 0); err != nil {
		return "", err
	}
	var buf [128]byte
	if err := ioctl(fd, syscall.TIOCPTYGNAME, uintptr(unsafe.Pointer(&buf[0]))); err != nil {
		return "", err
	}
	if i := bytes.IndexByte(buf[:], 0); i >= 0 {
		return string(buf[:i]), nil
	}
	return string(buf[:]), nil
}
