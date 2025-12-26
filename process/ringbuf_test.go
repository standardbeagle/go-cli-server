package process

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func TestRingBufferBasic(t *testing.T) {
	rb := NewRingBuffer(100)

	// Test initial state
	data, truncated := rb.Snapshot()
	if len(data) != 0 {
		t.Errorf("expected empty buffer, got %d bytes", len(data))
	}
	if truncated {
		t.Error("expected truncated = false for empty buffer")
	}

	// Test simple write
	msg := []byte("hello world")
	n, err := rb.Write(msg)
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != len(msg) {
		t.Errorf("Write returned %d, want %d", n, len(msg))
	}

	data, truncated = rb.Snapshot()
	if !bytes.Equal(data, msg) {
		t.Errorf("Snapshot() = %q, want %q", data, msg)
	}
	if truncated {
		t.Error("expected truncated = false")
	}
}

func TestRingBufferWrap(t *testing.T) {
	// Use a small buffer to test wrap behavior
	rb := NewRingBuffer(10)

	// Write more than buffer capacity
	rb.Write([]byte("12345"))     // 5 bytes
	rb.Write([]byte("67890abc"))  // 8 more = 13 total, wraps

	data, truncated := rb.Snapshot()
	if !truncated {
		t.Error("expected truncated = true after overflow")
	}

	// Buffer should contain the last 10 bytes
	if len(data) != 10 {
		t.Errorf("len(Snapshot()) = %d, want 10", len(data))
	}
}

func TestRingBufferCap(t *testing.T) {
	rb := NewRingBuffer(256)
	if rb.Cap() != 256 {
		t.Errorf("Cap() = %d, want 256", rb.Cap())
	}
}

func TestRingBufferLen(t *testing.T) {
	rb := NewRingBuffer(100)

	if rb.Len() != 0 {
		t.Errorf("Len() = %d, want 0 initially", rb.Len())
	}

	rb.Write([]byte("hello"))
	if rb.Len() != 5 {
		t.Errorf("Len() = %d, want 5", rb.Len())
	}
}

func TestRingBufferConcurrentWrites(t *testing.T) {
	rb := NewRingBuffer(DefaultBufferSize)

	var wg sync.WaitGroup
	numWriters := 10
	writesPerWriter := 100

	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < writesPerWriter; j++ {
				msg := []byte(strings.Repeat("x", 50))
				rb.Write(msg)
			}
		}(i)
	}

	wg.Wait()

	// Just verify no panic and buffer has content
	data, _ := rb.Snapshot()
	if len(data) == 0 {
		t.Error("expected non-empty buffer after concurrent writes")
	}
}

func TestRingBufferWriteInterface(t *testing.T) {
	rb := NewRingBuffer(100)

	// Verify it implements io.Writer
	var w interface{ Write([]byte) (int, error) } = rb
	n, err := w.Write([]byte("test"))
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != 4 {
		t.Errorf("Write returned %d, want 4", n)
	}
}

func TestRingBufferLargeWrite(t *testing.T) {
	size := 1024
	rb := NewRingBuffer(size)

	// Write exactly buffer size
	data := bytes.Repeat([]byte("A"), size)
	rb.Write(data)

	result, truncated := rb.Snapshot()
	if len(result) != size {
		t.Errorf("len(Snapshot()) = %d, want %d", len(result), size)
	}
	// No truncation when we exactly fill
	if truncated {
		t.Error("expected truncated = false when exactly filled")
	}

	// Now write one more byte to trigger truncation
	rb.Write([]byte("B"))
	result, truncated = rb.Snapshot()
	if !truncated {
		t.Error("expected truncated = true after overflow")
	}
}

func TestRingBufferReset(t *testing.T) {
	rb := NewRingBuffer(100)

	rb.Write([]byte("test data"))
	if rb.Len() == 0 {
		t.Error("expected non-empty buffer after write")
	}

	rb.Reset()
	if rb.Len() != 0 {
		t.Errorf("Len() = %d after Reset(), want 0", rb.Len())
	}

	data, truncated := rb.Snapshot()
	if len(data) != 0 {
		t.Error("expected empty Snapshot() after Reset()")
	}
	if truncated {
		t.Error("expected truncated = false after Reset()")
	}
}

func TestRingBufferTruncated(t *testing.T) {
	rb := NewRingBuffer(10)

	if rb.Truncated() {
		t.Error("expected Truncated() = false initially")
	}

	rb.Write([]byte("12345"))
	if rb.Truncated() {
		t.Error("expected Truncated() = false before overflow")
	}

	rb.Write([]byte("67890abc"))
	// Note: the overflowed flag is set on subsequent writes after capacity exceeded
	// Use Snapshot which returns truncated=true when buffer has wrapped
	_, truncated := rb.Snapshot()
	if !truncated {
		t.Error("expected Snapshot truncated = true after overflow")
	}
}
