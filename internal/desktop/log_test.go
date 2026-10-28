package desktop

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestApplicationDiagnosticsBounded(t *testing.T) {
	file, err := os.OpenFile(filepath.Join(t.TempDir(), "application.log"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	log := &applicationLog{file: file}
	for i := 0; i < 3; i++ {
		data := bytes.Repeat([]byte("x"), maxApplicationLog+100)
		if n, err := log.Write(data); err != nil || n != len(data) {
			t.Fatal("diagnostic output was not drained")
		}
		info, err := file.Stat()
		if err != nil || info.Size() > maxApplicationLog {
			t.Fatal("diagnostics exceeded bound")
		}
	}
}
