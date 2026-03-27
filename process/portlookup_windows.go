//go:build windows

package process

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// findPIDsByPort uses "netstat -ano" to find PIDs listening on the given port.
func findPIDsByPort(port int) []int {
	cmd := exec.Command("netstat", "-ano")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	var pids []int
	seen := make(map[int]struct{})
	portSuffix := fmt.Sprintf(":%d", port)

	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		// Match TCP lines in LISTENING state
		// Format: TCP    0.0.0.0:3000    0.0.0.0:0    LISTENING    1234
		if !strings.HasPrefix(line, "TCP") {
			continue
		}
		if !strings.Contains(line, "LISTENING") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		localAddr := fields[1]
		if !strings.HasSuffix(localAddr, portSuffix) {
			continue
		}
		pid, err := strconv.Atoi(fields[4])
		if err != nil || pid <= 0 {
			continue
		}
		if _, ok := seen[pid]; !ok {
			seen[pid] = struct{}{}
			pids = append(pids, pid)
		}
	}
	return pids
}
