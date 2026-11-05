//go:build darwin || linux

// Package term holds the few terminal calls agent-pty needs, on top of the
// syscall package: open a pty, read and set termios, copy the window size.
package term

import (
	"os"
	"syscall"
	"unsafe"
)

func ioctl(fd, req, arg uintptr) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg); e != 0 {
		return e
	}
	return nil
}

// Get returns the termios of fd. It fails when fd is not a terminal.
func Get(fd int) (*syscall.Termios, error) {
	var t syscall.Termios
	if err := ioctl(uintptr(fd), reqGet, uintptr(unsafe.Pointer(&t))); err != nil {
		return nil, err
	}
	return &t, nil
}

// Set applies t to fd at once.
func Set(fd int, t *syscall.Termios) error {
	return ioctl(uintptr(fd), reqSet, uintptr(unsafe.Pointer(t)))
}

// IsTerminal reports whether fd is a terminal.
func IsTerminal(fd int) bool {
	_, err := Get(fd)
	return err == nil
}

// Raw returns a copy of t in raw mode, as cfmakeraw(3) does.
func Raw(t *syscall.Termios) *syscall.Termios {
	r := *t
	r.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP |
		syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	r.Oflag &^= syscall.OPOST
	r.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	r.Cflag &^= syscall.CSIZE | syscall.PARENB
	r.Cflag |= syscall.CS8
	r.Cc[syscall.VMIN] = 1
	r.Cc[syscall.VTIME] = 0
	return &r
}

type winsize struct{ row, col, x, y uint16 }

// CopySize copies the window size of src to dst. Errors are ignored: a
// terminal without a size keeps the default.
func CopySize(src, dst int) {
	var w winsize
	if ioctl(uintptr(src), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&w))) == nil {
		_ = ioctl(uintptr(dst), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&w)))
	}
}

// SetSize sets the window size of fd.
func SetSize(fd int, rows, cols uint16) error {
	w := winsize{row: rows, col: cols}
	return ioctl(uintptr(fd), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&w)))
}

// SetForeground makes pgid the foreground process group of the terminal fd.
func SetForeground(fd, pgid int) error {
	p := int32(pgid)
	return ioctl(uintptr(fd), syscall.TIOCSPGRP, uintptr(unsafe.Pointer(&p)))
}

// Open returns the master and slave side of a new pty. Neither becomes the
// controlling terminal of the caller.
func Open() (master, slave *os.File, err error) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	name, err := unlock(m)
	if err != nil {
		m.Close()
		return nil, nil, err
	}
	s, err := os.OpenFile(name, os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		m.Close()
		return nil, nil, err
	}
	return m, s, nil
}
