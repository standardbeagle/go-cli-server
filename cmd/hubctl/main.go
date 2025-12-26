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
	"path/filepath"
	"strings"

	"github.com/standardbeagle/go-cli-server/protocol"
)

func main() {
	socketPath := flag.String("socket", "", "Hub socket path")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	// Determine socket path
	sock := *socketPath
	if sock == "" {
		// Try common locations
		candidates := []string{
			filepath.Join(os.TempDir(), "go-cli-server.sock"),
			"/tmp/go-cli-server.sock",
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				sock = c
				break
			}
		}
		if sock == "" {
			fmt.Fprintln(os.Stderr, "Error: could not find hub socket. Use --socket flag.")
			os.Exit(1)
		}
	}

	// Connect to hub
	conn, err := net.Dial("unix", sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to hub: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

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
	} else {
		fmt.Printf("Unexpected response: %s\n", resp.Type)
	}
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

	printResponse(resp)
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

	printResponse(resp)
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

	printResponse(resp)
}

func printResponse(resp *protocol.Response) {
	switch resp.Type {
	case protocol.ResponseOK:
		fmt.Printf("OK: %s\n", resp.Message)

	case protocol.ResponseErr:
		fmt.Printf("ERROR [%s]: %s\n", resp.Code, resp.Message)

	case protocol.ResponsePong:
		fmt.Println("PONG")

	case protocol.ResponseJSON:
		// Pretty print JSON
		var v interface{}
		if err := json.Unmarshal(resp.Data, &v); err != nil {
			fmt.Printf("JSON: %s\n", string(resp.Data))
		} else {
			formatted, _ := json.MarshalIndent(v, "", "  ")
			fmt.Println(string(formatted))
		}

	case protocol.ResponseData:
		fmt.Printf("DATA: %d bytes\n", len(resp.Data))

	default:
		fmt.Printf("Unknown response type: %s\n", resp.Type)
	}
}
