package sshx

import (
	"bytes"
	"testing"
)

func TestSafeBufferCapsOutput(t *testing.T) {
	var b safeBuffer
	chunk := bytes.Repeat([]byte("x"), 1<<20)
	for i := 0; i < 9; i++ {
		n, err := b.Write(chunk)
		if err != nil {
			t.Fatalf("Write: %v", err)
		}
		if n != len(chunk) {
			t.Fatalf("Write returned %d, want %d", n, len(chunk))
		}
	}
	if got := len(b.String()); got != maxSafeBufferBytes {
		t.Fatalf("buffer length = %d, want %d", got, maxSafeBufferBytes)
	}
}
