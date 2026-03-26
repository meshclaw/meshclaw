package vssh

import (
	"bufio"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	ChunkSize    = 1024 * 1024 // 1MB chunks
	SyncTimeout  = 3600        // 1 hour max for large files
)

// SyncFile uploads a file with chunked transfer, progress, and verification
func SyncFile(host string, port int, secret, localPath, remotePath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return err
	}
	size := stat.Size()

	// Calculate MD5
	hash := md5.New()
	if _, err := io.Copy(hash, f); err != nil {
		return err
	}
	md5sum := hex.EncodeToString(hash.Sum(nil))
	f.Seek(0, 0) // Reset to beginning

	// Connect
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), 30*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Auth
	token := GenerateAuthToken(secret)
	conn.Write([]byte(token + "\n"))

	reader := bufio.NewReader(conn)
	resp, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(resp, "AUTH_OK") {
		return fmt.Errorf("auth failed")
	}

	// Send SYNC command: SYNC <size> <md5> <path>
	if remotePath == "" {
		remotePath = filepath.Base(localPath)
	}
	cmd := fmt.Sprintf("SYNC %d %s %s\n", size, md5sum, remotePath)
	conn.Write([]byte(cmd))

	// Read response - might be SKIP (already exists with same MD5) or READY
	resp, err = reader.ReadString('\n')
	if err != nil {
		return err
	}
	resp = strings.TrimSpace(resp)

	if resp == "SKIP" {
		fmt.Printf("File already exists with same content, skipping\n")
		return nil
	}

	if !strings.HasPrefix(resp, "READY") {
		return fmt.Errorf("server error: %s", resp)
	}

	// Send file in chunks with progress
	sent := int64(0)
	buf := make([]byte, ChunkSize)
	startTime := time.Now()
	lastProgress := time.Now()

	for {
		n, err := f.Read(buf)
		if n > 0 {
			written, werr := conn.Write(buf[:n])
			if werr != nil {
				return fmt.Errorf("write error at %d/%d: %v", sent, size, werr)
			}
			sent += int64(written)

			// Progress every 1 second or every 100MB
			if time.Since(lastProgress) > time.Second || sent%(100*1024*1024) < int64(ChunkSize) {
				elapsed := time.Since(startTime).Seconds()
				speed := float64(sent) / elapsed / 1024 / 1024
				pct := float64(sent) / float64(size) * 100
				fmt.Printf("\r  %.1f%% (%d/%d MB) %.1f MB/s", pct, sent/1024/1024, size/1024/1024, speed)
				lastProgress = time.Now()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}

	fmt.Printf("\r  100%% (%d MB) - verifying...          \n", size/1024/1024)

	// Wait for verification response
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	resp, err = reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("verification timeout")
	}
	resp = strings.TrimSpace(resp)

	if strings.HasPrefix(resp, "OK") {
		elapsed := time.Since(startTime).Seconds()
		speed := float64(size) / elapsed / 1024 / 1024
		fmt.Printf("  Synced %d MB in %.1fs (%.1f MB/s)\n", size/1024/1024, elapsed, speed)
		return nil
	}

	return fmt.Errorf("verification failed: %s", resp)
}

// HandleSync handles SYNC command on server side
func HandleSync(conn net.Conn, cmd string) {
	// Parse: SYNC <size> <md5> <path>
	parts := strings.SplitN(cmd, " ", 4)
	if len(parts) < 4 {
		conn.Write([]byte("ERROR invalid command\n"))
		return
	}

	size, _ := strconv.ParseInt(parts[1], 10, 64)
	expectedMD5 := parts[2]
	path := strings.TrimSpace(parts[3])

	// Expand ~
	if len(path) > 0 && path[0] == '~' {
		home, _ := os.UserHomeDir()
		path = home + path[1:]
	}

	// Check if file exists with same MD5
	if existing, err := os.Open(path); err == nil {
		hash := md5.New()
		io.Copy(hash, existing)
		existing.Close()
		if hex.EncodeToString(hash.Sum(nil)) == expectedMD5 {
			conn.Write([]byte("SKIP\n"))
			return
		}
	}

	// Create directory
	dir := filepath.Dir(path)
	os.MkdirAll(dir, 0755)

	// Create temp file
	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		conn.Write([]byte(fmt.Sprintf("ERROR %v\n", err)))
		return
	}

	conn.Write([]byte("READY\n"))

	// Receive data
	hash := md5.New()
	writer := io.MultiWriter(f, hash)

	received := int64(0)
	buf := make([]byte, ChunkSize)
	conn.SetReadDeadline(time.Now().Add(time.Duration(SyncTimeout) * time.Second))

	for received < size {
		toRead := size - received
		if toRead > int64(ChunkSize) {
			toRead = int64(ChunkSize)
		}
		n, err := conn.Read(buf[:toRead])
		if err != nil {
			f.Close()
			os.Remove(tmpPath)
			conn.Write([]byte(fmt.Sprintf("ERROR read: %v\n", err)))
			return
		}
		writer.Write(buf[:n])
		received += int64(n)

		// Reset deadline on progress
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	}

	f.Close()

	// Verify MD5
	gotMD5 := hex.EncodeToString(hash.Sum(nil))
	if gotMD5 != expectedMD5 {
		os.Remove(tmpPath)
		conn.Write([]byte(fmt.Sprintf("ERROR md5 mismatch: expected %s, got %s\n", expectedMD5, gotMD5)))
		return
	}

	// Rename temp to final
	os.Remove(path)
	if err := os.Rename(tmpPath, path); err != nil {
		conn.Write([]byte(fmt.Sprintf("ERROR rename: %v\n", err)))
		return
	}

	conn.Write([]byte(fmt.Sprintf("OK %d %s\n", received, gotMD5)))
}
