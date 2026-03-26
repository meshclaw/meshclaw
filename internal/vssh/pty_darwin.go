//go:build darwin

package vssh

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

func openPty() (*os.File, *os.File, error) {
	pty, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, err
	}

	// Unlock
	var u int32 = 0
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, pty.Fd(), syscall.TIOCPTYUNLK, uintptr(unsafe.Pointer(&u)))
	if errno != 0 {
		pty.Close()
		return nil, nil, errno
	}

	// Get slave name
	buf := make([]byte, 128)
	_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, pty.Fd(), syscall.TIOCPTYGNAME, uintptr(unsafe.Pointer(&buf[0])))
	if errno != 0 {
		pty.Close()
		return nil, nil, errno
	}

	var slaveName string
	for i, b := range buf {
		if b == 0 {
			slaveName = string(buf[:i])
			break
		}
	}

	tty, err := os.OpenFile(slaveName, os.O_RDWR, 0)
	if err != nil {
		pty.Close()
		return nil, nil, err
	}

	return pty, tty, nil
}

type winsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

func setWinsize(f *os.File, rows, cols int) {
	ws := &winsize{Row: uint16(rows), Col: uint16(cols)}
	syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(ws)))
}

func getWinsize() (int, int) {
	ws := &winsize{}
	syscall.Syscall(syscall.SYS_IOCTL, os.Stdout.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(ws)))
	return int(ws.Row), int(ws.Col)
}

// Terminal state
type termState struct {
	termios syscall.Termios
}

func makeRaw(fd int) (*termState, error) {
	var oldState termState
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), syscall.TIOCGETA, uintptr(unsafe.Pointer(&oldState.termios)), 0, 0, 0)
	if errno != 0 {
		return nil, errno
	}

	newState := oldState.termios
	newState.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	newState.Oflag &^= syscall.OPOST
	newState.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	newState.Cflag &^= syscall.CSIZE | syscall.PARENB
	newState.Cflag |= syscall.CS8
	newState.Cc[syscall.VMIN] = 1
	newState.Cc[syscall.VTIME] = 0

	_, _, errno = syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), syscall.TIOCSETA, uintptr(unsafe.Pointer(&newState)), 0, 0, 0)
	if errno != 0 {
		return nil, errno
	}

	return &oldState, nil
}

func restoreTerminal(fd int, state *termState) {
	syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), syscall.TIOCSETA, uintptr(unsafe.Pointer(&state.termios)), 0, 0, 0)
}

// GetSecret is defined in auth.go
func init() {
	// Placeholder - GetSecret is in auth.go
	_ = fmt.Sprintf
}
