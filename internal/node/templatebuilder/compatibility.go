package templatebuilder

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func vmmDigest(binary string) (string, error) {
	path, err := exec.LookPath(binary)
	if err != nil {
		return "", err
	}
	return hashFile(path)
}
func cpuSignature() (string, error) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return "", errors.New("runtime snapshot compatibility currently requires Linux amd64")
	}
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return "", err
	}
	return hashCPUDescription(string(data))
}
func hashCPUDescription(cpuinfo string) (string, error) {
	fields := make(map[string]string)
	for line := range strings.SplitSeq(cpuinfo, "\n") {
		if strings.TrimSpace(line) == "" && len(fields) > 0 {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		switch key {
		case "vendor_id", "cpu family", "model", "stepping", "microcode", "flags", "address sizes":
			fields[key] = strings.Join(strings.Fields(value), " ")
		}
	}
	for _, key := range []string{"vendor_id", "cpu family", "model", "stepping", "flags"} {
		if fields[key] == "" {
			return "", errors.New("CPU compatibility description is incomplete")
		}
	}
	h := sha256.New()
	for _, key := range []string{"vendor_id", "cpu family", "model", "stepping", "microcode", "flags", "address sizes"} {
		io.WriteString(h, key+"="+fields[key]+"\n")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
