package protocol

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestValidateCommand(t *testing.T) {
	tests := []struct {
		name    string
		cmd     *Command
		wantErr bool
	}{
		{"plain", &Command{Verb: "PING"}, false},
		{"verb with space allowed", &Command{Verb: "SCRIPT GET dev"}, false},
		{"good args", &Command{Verb: "PROC", SubVerb: "STOP", Args: []string{"id-1"}}, false},
		{"terminator in arg", &Command{Verb: "PROC", Args: []string{"a;;b"}}, true},
		{"terminator in verb", &Command{Verb: "PI;;NG"}, true},
		{"newline in arg", &Command{Verb: "PROC", Args: []string{"a\nb"}}, true},
		{"space in arg", &Command{Verb: "PROC", Args: []string{"a b"}}, true},
		{"lone data marker arg", &Command{Verb: "PROC", Args: []string{"--"}}, true},
		{"empty verb", &Command{Verb: ""}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCommand(tt.cmd)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCommand() err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

// errAfterReader returns data, then a non-EOF error, simulating a deadline
// timeout that lands mid-frame.
type errAfterReader struct {
	data []byte
	pos  int
	err  error
}

func (r *errAfterReader) Read(p []byte) (int, error) {
	if r.pos < len(r.data) {
		n := copy(p, r.data[r.pos:])
		r.pos += n
		return n, nil
	}
	return 0, r.err
}

func TestPartialFrameError(t *testing.T) {
	// "ECHO hi" with no terminator, then a mid-frame read error.
	r := &errAfterReader{data: []byte("ECHO hi"), err: errors.New("i/o timeout")}
	p := NewParser(r)

	_, err := p.ParseCommand()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !IsPartialFrame(err) {
		t.Fatalf("expected PartialFrameError, got %T: %v", err, err)
	}
}

func TestCleanEOFNotPartialFrame(t *testing.T) {
	// No bytes buffered before EOF: this is a clean frame boundary, not a partial frame.
	p := NewParser(strings.NewReader(""))
	_, err := p.ParseCommand()
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
	if IsPartialFrame(err) {
		t.Fatal("clean EOF should not be a partial frame")
	}
}

func TestFrameTooLarge(t *testing.T) {
	// A reader that never emits a terminator.
	p := NewParser(&infiniteReader{b: 'A'})
	_, err := p.ParseCommand()
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("expected ErrFrameTooLarge, got %v", err)
	}
	if !IsPartialFrame(err) {
		t.Fatalf("expected oversized frame to be partial/desync, got %T", err)
	}
}

func TestValidateFrameSize(t *testing.T) {
	if err := ValidateFrameSize([]byte("PING;;")); err != nil {
		t.Fatalf("ValidateFrameSize small frame error = %v", err)
	}
	if err := ValidateFrameSize(make([]byte, MaxFrameSize+1)); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("ValidateFrameSize oversized error = %v, want ErrFrameTooLarge", err)
	}
}

func TestValidateCommandRejectsUnicodeWhitespaceArg(t *testing.T) {
	cmd := &Command{Verb: "RUN", Args: []string{"alpha\u00a0beta"}}
	if err := ValidateCommand(cmd); err == nil {
		t.Fatal("expected NBSP arg to be rejected")
	}
}

func TestFormatErrSanitizesDataMarkerAndCode(t *testing.T) {
	frame := string(FormatErr(ErrorCode("bad code;;"), "left -- right"))
	if strings.Contains(frame, "bad code") || strings.Contains(frame, ";; left") {
		t.Fatalf("error code was not sanitized: %q", frame)
	}
	if strings.Contains(frame, "left -- right") {
		t.Fatalf("message data marker was not sanitized: %q", frame)
	}
}

type infiniteReader struct{ b byte }

func (r *infiniteReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.b
	}
	return len(p), nil
}
