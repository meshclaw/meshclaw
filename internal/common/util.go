package common

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ANSI colors
const (
	Reset  = "\033[0m"
	Red    = "\033[31m"
	Green  = "\033[32m"
	Yellow = "\033[33m"
	Blue   = "\033[34m"
	Cyan   = "\033[36m"
	Dim    = "\033[2m"
)

// HomeDir returns user home directory
func HomeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}

// Run executes a command and returns stdout, stderr, exit code
func Run(name string, args ...string) (string, string, int) {
	cmd := exec.Command(name, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			code = 1
		}
	}
	return stdout.String(), stderr.String(), code
}

// RunShell executes a shell command
func RunShell(command string) (string, string, int) {
	return Run("sh", "-c", command)
}

// FileExists checks if a file exists
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// FindBin searches for a binary in common paths
func FindBin(name string) string {
	paths := []string{
		"/usr/bin/" + name,
		"/usr/local/bin/" + name,
		"/opt/homebrew/bin/" + name,
	}
	for _, p := range paths {
		if FileExists(p) {
			return p
		}
	}
	// Try PATH
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return name
}

// IsRoot checks if running as root
func IsRoot() bool {
	return os.Geteuid() == 0
}

// WireDir returns ~/.wire directory path
func WireDir() string {
	return filepath.Join(HomeDir(), ".wire")
}

// VsshDir returns ~/.vssh directory path
func VsshDir() string {
	return filepath.Join(HomeDir(), ".vssh")
}

// EnsureDir creates directory if not exists
func EnsureDir(path string) error {
	return os.MkdirAll(path, 0700)
}

// Print helpers
func PrintOK(msg string) {
	fmt.Printf("%s%s%s\n", Green, msg, Reset)
}

func PrintErr(msg string) {
	fmt.Fprintf(os.Stderr, "%s%s%s\n", Red, msg, Reset)
}

func PrintWarn(msg string) {
	fmt.Printf("%s%s%s\n", Yellow, msg, Reset)
}
