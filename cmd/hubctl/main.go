// Package main provides a CLI client for the go-cli-server hub.
//
// Usage:
//
//	# Send a PING command
//	hubctl ping
//
//	# List registered subprocesses
//	hubctl subprocess list
//
//	# Get subprocess status
//	hubctl subprocess status <id>
//
//	# Send a raw command
//	hubctl raw "PING;;"
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/standardbeagle/go-cli-server/protocol"
	"github.com/standardbeagle/go-cli-server/socket"
)

func main() {
	socketPath := flag.String("socket", "", "Hub socket path")
	socketName := flag.String("name", socket.DefaultSocketName, "Socket name (must match the hub's -name)")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	// Determine socket path. When not given explicitly, derive the same default
	// the hub uses (socket.DefaultSocketPath) so hubctl and the hub agree on the
	// path — $XDG_RUNTIME_DIR/<name>.sock or /tmp/<name>-<uid>/<name>.sock.
	sock := *socketPath
	if sock == "" {
		sock = socket.DefaultSocketPath(*socketName)
		if _, err := os.Stat(sock); err != nil {
			fmt.Fprintf(os.Stderr, "Error: could not find hub socket at %s. Use --socket or --name.\n", sock)
			os.Exit(1)
		}
	}

	// Connect to hub
	conn, err := net.DialTimeout("unix", sock, 5*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to hub: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	// Bound the single request/response so a wedged hub cannot hang hubctl forever.
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	parser := protocol.NewParser(conn)
	writer := protocol.NewWriter(conn)

	// Execute command
	cmd := args[0]
	cmdArgs := args[1:]

	switch cmd {
	case "ping":
		execPing(writer, parser)

	case "subprocess", "sp":
		if len(cmdArgs) == 0 {
			fmt.Fprintln(os.Stderr, "Error: subprocess command requires action")
			os.Exit(1)
		}
		execSubprocess(writer, parser, cmdArgs)

	case "raw":
		if len(cmdArgs) == 0 {
			fmt.Fprintln(os.Stderr, "Error: raw command requires data")
			os.Exit(1)
		}
		execRaw(conn, parser, cmdArgs[0])

	case "info":
		execInfo(writer, parser)

	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: hubctl [options] <command> [args...]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  ping                   Send PING, expect PONG")
	fmt.Println("  info                   Get hub info")
	fmt.Println("  subprocess list        List registered subprocesses")
	fmt.Println("  subprocess status <id> Get subprocess status")
	fmt.Println("  raw <data>             Send raw protocol data")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  --socket <path>   Hub socket path")
	fmt.Printf("  --name <name>     Socket name for the default path (default %s)\n", socket.DefaultSocketName)
}

func execPing(writer *protocol.Writer, parser *protocol.Parser) {
	if err := writer.WriteCommand("PING", nil, nil); err != nil {
		fmt.Fprintf(os.Stderr, "Error sending PING: %v\n", err)
		os.Exit(1)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading response: %v\n", err)
		os.Exit(1)
	}

	if resp.Type == protocol.ResponsePong {
		fmt.Println("PONG")
		return
	}
	fmt.Printf("Unexpected response: %s\n", resp.Type)
	os.Exit(1)
}

func execInfo(writer *protocol.Writer, parser *protocol.Parser) {
	if err := writer.WriteCommand("INFO", nil, nil); err != nil {
		fmt.Fprintf(os.Stderr, "Error sending INFO: %v\n", err)
		os.Exit(1)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading response: %v\n", err)
		os.Exit(1)
	}

	os.Exit(printResponse(resp))
}

func execSubprocess(writer *protocol.Writer, parser *protocol.Parser, args []string) {
	action := strings.ToUpper(args[0])
	subArgs := args[1:]

	switch action {
	case "LIST":
		if err := writer.WriteCommandWithSubVerb("SUBPROCESS", "LIST", nil, nil); err != nil {
			fmt.Fprintf(os.Stderr, "Error sending command: %v\n", err)
			os.Exit(1)
		}

	case "STATUS":
		if len(subArgs) == 0 {
			fmt.Fprintln(os.Stderr, "Error: subprocess status requires ID")
			os.Exit(1)
		}
		if err := writer.WriteCommandWithSubVerb("SUBPROCESS", "STATUS", subArgs, nil); err != nil {
			fmt.Fprintf(os.Stderr, "Error sending command: %v\n", err)
			os.Exit(1)
		}

	case "START":
		if len(subArgs) == 0 {
			fmt.Fprintln(os.Stderr, "Error: subprocess start requires ID")
			os.Exit(1)
		}
		if err := writer.WriteCommandWithSubVerb("SUBPROCESS", "START", subArgs, nil); err != nil {
			fmt.Fprintf(os.Stderr, "Error sending command: %v\n", err)
			os.Exit(1)
		}

	case "STOP":
		if len(subArgs) == 0 {
			fmt.Fprintln(os.Stderr, "Error: subprocess stop requires ID")
			os.Exit(1)
		}
		if err := writer.WriteCommandWithSubVerb("SUBPROCESS", "STOP", subArgs, nil); err != nil {
			fmt.Fprintf(os.Stderr, "Error sending command: %v\n", err)
			os.Exit(1)
		}

	case "UNREGISTER":
		if len(subArgs) == 0 {
			fmt.Fprintln(os.Stderr, "Error: subprocess unregister requires ID")
			os.Exit(1)
		}
		if err := writer.WriteCommandWithSubVerb("SUBPROCESS", "UNREGISTER", subArgs, nil); err != nil {
			fmt.Fprintf(os.Stderr, "Error sending command: %v\n", err)
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "Unknown subprocess action: %s\n", action)
		os.Exit(1)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading response: %v\n", err)
		os.Exit(1)
	}

	os.Exit(printResponse(resp))
}

func execRaw(conn net.Conn, parser *protocol.Parser, data string) {
	// Ensure data ends with terminator
	if !strings.HasSuffix(data, ";;") {
		data += ";;"
	}

	_, err := conn.Write([]byte(data))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error sending data: %v\n", err)
		os.Exit(1)
	}

	resp, err := parser.ParseResponse()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading response: %v\n", err)
		os.Exit(1)
	}

	os.Exit(printResponse(resp))
}

// printResponse prints the response and returns the process exit code: nonzero
// on protocol errors / unknown types so scripts can detect failure.
func printResponse(resp *protocol.Response) int {
	switch resp.Type {
	case protocol.ResponseOK:
		fmt.Printf("OK: %s\n", resp.Message)
		return 0

	case protocol.ResponseErr:
		fmt.Printf("ERROR [%s]: %s\n", resp.Code, resp.Message)
		return 1

	case protocol.ResponsePong:
		fmt.Println("PONG")
		return 0

	case protocol.ResponseJSON:
		// Pretty print JSON
		var v interface{}
		if err := json.Unmarshal(resp.Data, &v); err != nil {
			fmt.Printf("JSON: %s\n", string(resp.Data))
		} else {
			formatted, _ := json.MarshalIndent(v, "", "  ")
			fmt.Println(string(formatted))
		}
		return 0

	case protocol.ResponseData:
		fmt.Printf("DATA: %d bytes\n", len(resp.Data))
		return 0

	default:
		fmt.Printf("Unknown response type: %s\n", resp.Type)
		return 1
	}
}
