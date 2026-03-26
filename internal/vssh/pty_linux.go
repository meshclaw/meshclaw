//go:build linux

package vssh

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
	"unsafe"
)

const (
	TIOCGPTN   = 0x80045430
	TIOCSPTLCK = 0x40045431
	TCGETS     = 0x5401
	TCSETS     = 0x5402
)

func openPty() (*os.File, *os.File, error) {
	pty, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}

	// Unlock
	var unlock int32 = 0
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, pty.Fd(), TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock)))
	if errno != 0 {
		pty.Close()
		return nil, nil, errno
	}

	// Get pty number
	var ptyNum uint32
	_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, pty.Fd(), TIOCGPTN, uintptr(unsafe.Pointer(&ptyNum)))
	if errno != 0 {
		pty.Close()
		return nil, nil, errno
	}

	slaveName := "/dev/pts/" + strconv.Itoa(int(ptyNum))
	tty, err := os.OpenFile(slaveName, os.O_RDWR|syscall.O_NOCTTY, 0)
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

// Linux termios
type termios struct {
	Iflag  uint32
	Oflag  uint32
	Cflag  uint32
	Lflag  uint32
	Line   uint8
	Cc     [19]uint8
	Ispeed uint32
	Ospeed uint32
}

type termState struct {
	termios termios
}

func makeRaw(fd int) (*termState, error) {
	var oldState termState
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), TCGETS, uintptr(unsafe.Pointer(&oldState.termios)))
	if errno != 0 {
		return nil, errno
	}

	newState := oldState.termios
	// Input flags
	newState.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	// Output flags
	newState.Oflag &^= syscall.OPOST
	// Local flags
	newState.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	// Control flags
	newState.Cflag &^= syscall.CSIZE | syscall.PARENB
	newState.Cflag |= syscall.CS8
	newState.Cc[syscall.VMIN] = 1
	newState.Cc[syscall.VTIME] = 0

	_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), TCSETS, uintptr(unsafe.Pointer(&newState)))
	if errno != 0 {
		return nil, errno
	}

	return &oldState, nil
}

func restoreTerminal(fd int, state *termState) {
	syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), TCSETS, uintptr(unsafe.Pointer(&state.termios)))
}

func init() {
	_ = fmt.Sprintf
}
