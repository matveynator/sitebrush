package crawler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"os"
	"strings"
	"testing"
)

type zeroResourceReader struct{ remain int64 }

func (r *zeroResourceReader) Read(p []byte) (int, error) {
	if r.remain == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > r.remain {
		n = int(r.remain)
	}
	clear(p[:n])
	r.remain -= int64(n)
	return n, nil
}

func TestStreamImportResourceLargerThanOld64MiBLimit(t *testing.T) {
	const size = int64(64<<20) + 129
	reader := &zeroResourceReader{remain: size}
	var reported int64
	resource, err := StreamImportResource(t.Context().Done(), reader, t.TempDir(), size, size+100, func(n int64) { reported = n })
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(resource.Path)
	if resource.Size != size || reported != size {
		t.Fatalf("received size=%d progress=%d, want %d", resource.Size, reported, size)
	}
	info, err := os.Stat(resource.Path)
	if err != nil || info.Size() != size {
		t.Fatalf("streamed file: info=%v error=%v", info, err)
	}
	if len(resource.FirstBytes) != 512 {
		t.Fatalf("sniffed sample length = %d", len(resource.FirstBytes))
	}
}

func TestStreamImportResourceChecksUnknownLengthAgainstQuota(t *testing.T) {
	_, err := StreamImportResource(t.Context().Done(), strings.NewReader("one-two-three"), t.TempDir(), -1, 5, nil)
	if err == nil || !strings.Contains(err.Error(), "site quota") {
		t.Fatalf("expected quota failure, got %v", err)
	}
	_, err = StreamImportResource(t.Context().Done(), strings.NewReader("one-two-three"), t.TempDir(), 13, 5, nil)
	if err == nil || !strings.Contains(err.Error(), "site quota") {
		t.Fatalf("expected content-length quota failure, got %v", err)
	}
}

func TestStreamImportResourceHashesExactBytes(t *testing.T) {
	body := []byte("a short mp4 fixture")
	resource, err := StreamImportResource(t.Context().Done(), bytes.NewReader(body), t.TempDir(), -1, int64(len(body)), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(resource.Path)
	want := sha256.Sum256(body)
	if resource.SHA256 != want || resource.Size != int64(len(body)) || !bytes.Equal(resource.FirstBytes, body) {
		t.Fatalf("wrong streamed resource metadata")
	}
}

func TestStreamImportResourceRejectsInterruptedTransfer(t *testing.T) {
	stageDirectory := t.TempDir()
	_, err := StreamImportResource(t.Context().Done(), strings.NewReader("123"), stageDirectory, 4, 100, nil)
	if err == nil || !strings.Contains(err.Error(), "size mismatch") {
		t.Fatalf("expected truncated body error, got %v", err)
	}
	entries, err := os.ReadDir(stageDirectory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("interrupted transfer left staged files: entries=%v err=%v", entries, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = StreamImportResource(ctx.Done(), strings.NewReader("123"), t.TempDir(), 3, 100, nil)
	if err == nil || err != context.Canceled {
		t.Fatalf("expected canceled context, got %v", err)
	}
}
