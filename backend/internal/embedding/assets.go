package embedding

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"

	"mailpilot.local/core/internal/embedding/model"
)

// Resource resolution is derived from the running fixed sidecar, never a CLI,
// WebView, environment variable, current directory or caller-supplied model path.
func resourceDirectory() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", ErrUnavailable
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", ErrUnavailable
	}
	return resourcesForExecutable(exe)
}
func resourcesForExecutable(exe string) (string, error) {
	if !filepath.IsAbs(exe) {
		return "", ErrUnavailable
	}
	parent := filepath.Dir(exe)
	name := filepath.Base(exe)
	if name == "mailpilot-core" && filepath.Base(parent) == "MacOS" && filepath.Base(filepath.Dir(parent)) == "Contents" {
		return filepath.Join(filepath.Dir(parent), "Resources", "embedding"), nil
	}
	if name == "mailpilot-core-aarch64-apple-darwin" && filepath.Base(parent) == "binaries" && filepath.Base(filepath.Dir(parent)) == "src-tauri" {
		return filepath.Join(filepath.Dir(parent), "resources", "embedding"), nil
	}
	return "", ErrUnavailable
}

// Reject symlink resources or symlink parents beneath the trusted resource root.
func regularResource(root, name string) (string, error) {
	if name != "model.onnx" && name != "libonnxruntime.dylib" {
		return "", ErrUnavailable
	}
	root = filepath.Clean(root)
	for path := root; ; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", ErrUnavailable
		}
		if filepath.Dir(path) == path {
			break
		}
	}
	path := filepath.Join(root, name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", ErrUnavailable
	}
	return path, nil
}
func readResource(ctx context.Context, root, name string, minBytes, maxBytes int64) ([]byte, string, error) {
	path, err := regularResource(root, name)
	if err != nil {
		return nil, "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, "", ErrUnavailable
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() < minBytes || info.Size() > maxBytes {
		return nil, "", ErrUnavailable
	}
	data := make([]byte, info.Size())
	for offset := 0; offset < len(data); {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		n, err := io.ReadFull(f, data[offset:min(offset+1024*1024, len(data))])
		offset += n
		if err != nil {
			return nil, "", ErrUnavailable
		}
	}
	return data, path, nil
}
func verifiedAssets(ctx context.Context, root string) ([]byte, string, error) {
	m := model.Artifacts()
	// A release signature can grow the tail slightly without changing code/data.
	library, path, err := readResource(ctx, root, "libonnxruntime.dylib", m.LibraryBytes-1024*1024, m.LibraryBytes+1024*1024)
	if err != nil {
		return nil, "", err
	}
	digest, err := machoFingerprint(library)
	if err != nil || digest != m.LibraryCodeSHA256 {
		return nil, "", ErrUnavailable
	}
	data, _, err := readResource(ctx, root, "model.onnx", m.ModelBytes, m.ModelBytes)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != m.ModelSHA256 {
		return nil, "", ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	return data, path, nil
}

// Hash every pre-signature byte of the arm64 Mach-O, normalizing only the
// signature length and __LINKEDIT virtual/file sizes changed by codesign. The
// signature offset remains hashed, so ignored bytes cannot be moved into code.
// The OS independently validates the actual signature when loading the library.
func machoFingerprint(data []byte) (string, error) {
	if len(data) < 32 || binary.LittleEndian.Uint32(data) != 0xfeedfacf || binary.LittleEndian.Uint32(data[4:]) != 0x100000c {
		return "", ErrUnavailable
	}
	commands := int(binary.LittleEndian.Uint32(data[16:]))
	size := int(binary.LittleEndian.Uint32(data[20:]))
	if commands < 1 || commands > 256 || size > len(data)-32 {
		return "", ErrUnavailable
	}
	cut := 0
	offset := 32
	signatureLengthOffset := 0
	linkeditOffset := 0
	for i := 0; i < commands; i++ {
		if offset+8 > 32+size {
			return "", ErrUnavailable
		}
		cmd := binary.LittleEndian.Uint32(data[offset:])
		length := int(binary.LittleEndian.Uint32(data[offset+4:]))
		if length < 8 || offset+length > 32+size {
			return "", ErrUnavailable
		}
		if cmd == 0x1d {
			if length != 16 || cut != 0 {
				return "", ErrUnavailable
			}
			cut = int(binary.LittleEndian.Uint32(data[offset+8:]))
			signatureSize := int(binary.LittleEndian.Uint32(data[offset+12:]))
			if cut < 32+size || cut > len(data) || signatureSize != len(data)-cut {
				return "", ErrUnavailable
			}
			signatureLengthOffset = offset + 12
		}
		if cmd == 0x19 && length >= 72 && strings.TrimRight(string(data[offset+8:offset+24]), "\x00") == "__LINKEDIT" {
			linkeditOffset = offset
		}
		offset += length
	}
	if offset != 32+size || cut == 0 || linkeditOffset == 0 {
		return "", ErrUnavailable
	}
	// Modify only the private read buffer; it is never passed to dlopen.
	clear(data[signatureLengthOffset : signatureLengthOffset+4])
	clear(data[linkeditOffset+32 : linkeditOffset+40])
	clear(data[linkeditOffset+48 : linkeditOffset+56])
	sum := sha256.Sum256(data[:cut])
	return hex.EncodeToString(sum[:]), nil
}
