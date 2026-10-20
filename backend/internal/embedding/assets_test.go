package embedding

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFixedResourcePathsAndSymlinkRejection(t *testing.T) {
	for _, name := range []string{"/tmp/model.onnx", "/tmp/mailpilot-core", "/tmp/Contents/MacOS/arbitrary-executable", "relative/path", "/tmp/libonnxruntime.dylib"} {
		if _, err := resourcesForExecutable(name); !errors.Is(err, ErrUnavailable) {
			t.Fatal("untrusted resource root accepted")
		}
	}
	if root, err := resourcesForExecutable("/Applications/MailPilot.app/Contents/MacOS/mailpilot-core"); err != nil || root != "/Applications/MailPilot.app/Contents/Resources/embedding" {
		t.Fatal("bundle resource resolution", err)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "source")
	if err = os.WriteFile(target, []byte("not a model"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(target, filepath.Join(dir, "model.onnx")); err != nil {
		t.Fatal(err)
	}
	if _, _, err = readResource(context.Background(), dir, "model.onnx", 1, 100); !errors.Is(err, ErrUnavailable) {
		t.Fatal("symlink model accepted", err)
	}
	if _, err = regularResource(dir, "../source"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("path traversal accepted", err)
	}
}
func fixtureMachO() []byte {
	b := make([]byte, 512)
	binary.LittleEndian.PutUint32(b, 0xfeedfacf)
	binary.LittleEndian.PutUint32(b[4:], 0x100000c)
	binary.LittleEndian.PutUint32(b[16:], 2)
	binary.LittleEndian.PutUint32(b[20:], 88)
	binary.LittleEndian.PutUint32(b[32:], 0x19)
	binary.LittleEndian.PutUint32(b[36:], 72)
	copy(b[40:], "__LINKEDIT")
	binary.LittleEndian.PutUint32(b[104:], 0x1d)
	binary.LittleEndian.PutUint32(b[108:], 16)
	binary.LittleEndian.PutUint32(b[112:], 400)
	binary.LittleEndian.PutUint32(b[116:], 112)
	b[200] = 7
	return b
}
func TestMachOFingerprintAllowsSigningButRejectsCodeChanges(t *testing.T) {
	expected, err := machoFingerprint(fixtureMachO())
	if err != nil {
		t.Fatal(err)
	}
	signed := append(fixtureMachO(), make([]byte, 50)...)
	binary.LittleEndian.PutUint32(signed[116:], 162)
	signed[450] = 42
	binary.LittleEndian.PutUint64(signed[64:], 2048)
	binary.LittleEndian.PutUint64(signed[80:], 1024)
	got, err := machoFingerprint(signed)
	if err != nil || got != expected {
		t.Fatal("signature-only mutation rejected", err)
	}
	tampered := fixtureMachO()
	tampered[200]++
	got, err = machoFingerprint(tampered)
	if err != nil || got == expected {
		t.Fatal("code tampering ignored", err)
	}
	for _, data := range [][]byte{nil, make([]byte, 32), fixtureMachO()[:200]} {
		if _, err = machoFingerprint(data); !errors.Is(err, ErrUnavailable) {
			t.Fatal("malformed runtime accepted")
		}
	}
}
