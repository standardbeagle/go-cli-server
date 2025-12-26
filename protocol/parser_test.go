package protocol

import (
	"bytes"
	"testing"
)

func TestParseSimpleCommand(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantVerb   string
		wantSubVer string
		wantArgs   []string
		wantErr    bool
	}{
		{
			name:     "ping command",
			input:    "PING;;",
			wantVerb: "PING",
		},
		{
			name:     "info command",
			input:    "INFO;;",
			wantVerb: "INFO",
		},
		{
			name:       "proc status command",
			input:      "PROC STATUS process-1;;",
			wantVerb:   "PROC",
			wantSubVer: "STATUS",
			wantArgs:   []string{"process-1"},
		},
		{
			name:       "proc list command",
			input:      "PROC LIST;;",
			wantVerb:   "PROC",
			wantSubVer: "LIST",
		},
		{
			name:       "proc output with args",
			input:      "PROC OUTPUT proc-1 tail=50;;",
			wantVerb:   "PROC",
			wantSubVer: "OUTPUT",
			wantArgs:   []string{"proc-1", "tail=50"},
		},
		{
			name:    "unknown command",
			input:   "FOOBAR;;",
			wantErr: true,
		},
		{
			name:    "json instead of command",
			input:   `{"verb": "PING"};;`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := NewParser(bytes.NewReader([]byte(tt.input)))
			cmd, err := parser.ParseCommand()

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if cmd.Verb != tt.wantVerb {
				t.Errorf("Verb = %q, want %q", cmd.Verb, tt.wantVerb)
			}

			if cmd.SubVerb != tt.wantSubVer {
				t.Errorf("SubVerb = %q, want %q", cmd.SubVerb, tt.wantSubVer)
			}

			if len(cmd.Args) != len(tt.wantArgs) {
				t.Errorf("Args len = %d, want %d", len(cmd.Args), len(tt.wantArgs))
			} else {
				for i, arg := range cmd.Args {
					if arg != tt.wantArgs[i] {
						t.Errorf("Args[%d] = %q, want %q", i, arg, tt.wantArgs[i])
					}
				}
			}
		})
	}
}

func TestParseCommandWithData(t *testing.T) {
	// Build a command with base64-encoded JSON data
	jsonData := []byte(`{"key": "value"}`)
	cmd := &Command{
		Verb:    "RUN-JSON",
		Args:    nil,
		Data:    jsonData,
	}
	encoded := FormatCommand(cmd)

	parser := NewParser(bytes.NewReader(encoded))
	parsed, err := parser.ParseCommand()
	if err != nil {
		t.Fatalf("ParseCommand error: %v", err)
	}

	if parsed.Verb != "RUN-JSON" {
		t.Errorf("Verb = %q, want %q", parsed.Verb, "RUN-JSON")
	}

	if !bytes.Equal(parsed.Data, jsonData) {
		t.Errorf("Data = %q, want %q", parsed.Data, jsonData)
	}
}

func TestFormatCommand(t *testing.T) {
	tests := []struct {
		name     string
		cmd      *Command
		contains []string
	}{
		{
			name: "simple command",
			cmd: &Command{
				Verb: "PING",
			},
			contains: []string{"PING", ";;"},
		},
		{
			name: "command with subverb and args",
			cmd: &Command{
				Verb:    "PROC",
				SubVerb: "STATUS",
				Args:    []string{"process-1"},
			},
			contains: []string{"PROC", "STATUS", "process-1", ";;"},
		},
		{
			name: "command with data",
			cmd: &Command{
				Verb: "RUN-JSON",
				Data: []byte("test data"),
			},
			contains: []string{"RUN-JSON", "--", ";;"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := string(FormatCommand(tt.cmd))
			for _, s := range tt.contains {
				if !bytes.Contains([]byte(result), []byte(s)) {
					t.Errorf("result %q does not contain %q", result, s)
				}
			}
		})
	}
}

func TestParseResponse(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantType   ResponseType
		wantMsg    string
		wantCode   string
		wantHasErr bool
	}{
		{
			name:     "ok response",
			input:    "OK;;",
			wantType: ResponseOK,
		},
		{
			name:     "ok with message",
			input:    "OK process started;;",
			wantType: ResponseOK,
			wantMsg:  "process started",
		},
		{
			name:     "pong response",
			input:    "PONG;;",
			wantType: ResponsePong,
		},
		{
			name:     "error response",
			input:    "ERR NOT_FOUND process not found;;",
			wantType: ResponseErr,
			wantCode: "NOT_FOUND",
			wantMsg:  "process not found",
		},
		{
			name:     "end response",
			input:    "END;;",
			wantType: ResponseEnd,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := NewParser(bytes.NewReader([]byte(tt.input)))
			resp, err := parser.ParseResponse()

			if tt.wantHasErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if resp.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", resp.Type, tt.wantType)
			}

			if resp.Message != tt.wantMsg {
				t.Errorf("Message = %q, want %q", resp.Message, tt.wantMsg)
			}

			if resp.Code != tt.wantCode {
				t.Errorf("Code = %q, want %q", resp.Code, tt.wantCode)
			}
		})
	}
}

func TestWriter(t *testing.T) {
	buf := &bytes.Buffer{}
	w := NewWriter(buf)

	t.Run("WriteOK", func(t *testing.T) {
		buf.Reset()
		if err := w.WriteOK("success"); err != nil {
			t.Fatalf("WriteOK error: %v", err)
		}
		if !bytes.Contains(buf.Bytes(), []byte("OK success;;")) {
			t.Errorf("output = %q, want to contain 'OK success;;'", buf.String())
		}
	})

	t.Run("WritePong", func(t *testing.T) {
		buf.Reset()
		if err := w.WritePong(); err != nil {
			t.Fatalf("WritePong error: %v", err)
		}
		if !bytes.Contains(buf.Bytes(), []byte("PONG;;")) {
			t.Errorf("output = %q, want to contain 'PONG;;'", buf.String())
		}
	})

	t.Run("WriteErr", func(t *testing.T) {
		buf.Reset()
		if err := w.WriteErr(ErrNotFound, "resource not found"); err != nil {
			t.Fatalf("WriteErr error: %v", err)
		}
		result := buf.String()
		if !bytes.Contains(buf.Bytes(), []byte("ERR")) {
			t.Errorf("output = %q, want to contain 'ERR'", result)
		}
	})
}

func TestVerbRegistry(t *testing.T) {
	reg := NewVerbRegistry()

	// Test built-in verbs
	if !reg.IsValidVerb("PING") {
		t.Error("expected PING to be valid")
	}
	if !reg.IsValidVerb("ping") {
		t.Error("expected lowercase ping to be valid")
	}
	if !reg.IsValidVerb("PROC") {
		t.Error("expected PROC to be valid")
	}

	// Test custom verb registration
	reg.RegisterVerb("CUSTOM")
	if !reg.IsValidVerb("CUSTOM") {
		t.Error("expected CUSTOM to be valid after registration")
	}

	// Test sub-verbs
	if !reg.IsSubVerb("STATUS") {
		t.Error("expected STATUS to be a valid sub-verb")
	}
	if !reg.IsSubVerb("status") {
		t.Error("expected lowercase status to be valid")
	}

	reg.RegisterSubVerb("MYCUSTOM")
	if !reg.IsSubVerb("MYCUSTOM") {
		t.Error("expected MYCUSTOM to be valid after registration")
	}
}

func TestRoundTrip(t *testing.T) {
	// Test that commands can be formatted and parsed back
	original := &Command{
		Verb:    "PROC",
		SubVerb: "STATUS",
		Args:    []string{"test-process", "extra-arg"},
	}

	formatted := FormatCommand(original)
	parser := NewParser(bytes.NewReader(formatted))
	parsed, err := parser.ParseCommand()
	if err != nil {
		t.Fatalf("ParseCommand error: %v", err)
	}

	if parsed.Verb != original.Verb {
		t.Errorf("Verb = %q, want %q", parsed.Verb, original.Verb)
	}
	if parsed.SubVerb != original.SubVerb {
		t.Errorf("SubVerb = %q, want %q", parsed.SubVerb, original.SubVerb)
	}
	if len(parsed.Args) != len(original.Args) {
		t.Fatalf("Args len = %d, want %d", len(parsed.Args), len(original.Args))
	}
	for i := range parsed.Args {
		if parsed.Args[i] != original.Args[i] {
			t.Errorf("Args[%d] = %q, want %q", i, parsed.Args[i], original.Args[i])
		}
	}
}

func TestRoundTripWithData(t *testing.T) {
	jsonData := []byte(`{"command": "npm", "args": ["start"], "path": "/project"}`)
	original := &Command{
		Verb: "RUN-JSON",
		Data: jsonData,
	}

	formatted := FormatCommand(original)
	parser := NewParser(bytes.NewReader(formatted))
	parsed, err := parser.ParseCommand()
	if err != nil {
		t.Fatalf("ParseCommand error: %v", err)
	}

	if parsed.Verb != original.Verb {
		t.Errorf("Verb = %q, want %q", parsed.Verb, original.Verb)
	}
	if !bytes.Equal(parsed.Data, original.Data) {
		t.Errorf("Data = %q, want %q", parsed.Data, original.Data)
	}
}
