package main

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/standardbeagle/go-cli-server/protocol"
)

type recordingConn struct {
	written []byte
}

func (c *recordingConn) Read([]byte) (int, error) { return 0, errors.New("unused") }
func (c *recordingConn) Write(p []byte) (int, error) {
	c.written = append(c.written, p...)
	return len(p), nil
}
func (c *recordingConn) Close() error                     { return nil }
func (c *recordingConn) LocalAddr() net.Addr              { return nil }
func (c *recordingConn) RemoteAddr() net.Addr             { return nil }
func (c *recordingConn) SetDeadline(time.Time) error      { return nil }
func (c *recordingConn) SetReadDeadline(time.Time) error  { return nil }
func (c *recordingConn) SetWriteDeadline(time.Time) error { return nil }

func TestWriteRawFrameAddsTerminator(t *testing.T) {
	conn := &recordingConn{}
	if err := writeRawFrame(conn, "PING"); err != nil {
		t.Fatalf("writeRawFrame() error = %v", err)
	}
	if string(conn.written) != "PING;;" {
		t.Fatalf("written frame = %q, want PING;;", string(conn.written))
	}
}

func TestWriteRawFrameRejectsOversizedFrame(t *testing.T) {
	conn := &recordingConn{}
	err := writeRawFrame(conn, string(make([]byte, protocol.MaxFrameSize+1)))
	if !errors.Is(err, protocol.ErrFrameTooLarge) {
		t.Fatalf("writeRawFrame() error = %v, want ErrFrameTooLarge", err)
	}
	if len(conn.written) != 0 {
		t.Fatalf("oversized frame wrote %d bytes", len(conn.written))
	}
}
