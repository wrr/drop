package netns

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/wrr/drop/internal/config"
)

// portMappingArgs returns pasta port mapping arguments. If the mappings list
// is empty, the port mapping is disabled with the "none"
// argument. Each mapping is translated to a separate pasta argument,
// for example: --tcp-ports 80:8000 --tcp-ports 5000
func portMappingArgs(argName string, mappings []string) []string {
	if len(mappings) == 0 {
		return []string{argName, "none"}
	}

	var result []string
	for _, mapping := range mappings {
		result = append(result, argName, mapping)
	}
	return result
}

// StartPasta starts pasta to provide network connectivity
// within a network namespace and configures port forwarding.
//
// Returns a cleanup function that should be called when program exits.
func StartPasta(jailedPid int, netConfig config.Net, runDir string) (func(), error) {
	var pastaArgs []string

	// File where pasta writes deamon child process pid after network
	// setup is done.
	pidPath := filepath.Join(runDir, "pasta.pid")
	logPath := filepath.Join(runDir, "pasta.log")

	pastaArgs = []string{
		"--config-net",
		// Address to be used in the namespace as DNS. Pasta forwards DNS
		// requests to this address to the actual host DNS.
		"--dns-forward", "10.0.2.3",
		"--pid", pidPath,
		"--no-map-gw",
		"--log-file", logPath,
	}

	// TCP ports open in the namespace that are accessible from the host.
	pastaArgs = append(pastaArgs, portMappingArgs("--tcp-ports", netConfig.TCPPortsToHost)...)
	// TCP ports open on the host that are accessible from the namespace.
	// This mapping is also needed to allow drop instances to connect
	// to one another (one instance exposes a port to host with
	// --tcp-port and the other needs --tcp-ns to be able connect to
	// this port.
	pastaArgs = append(pastaArgs, portMappingArgs("--tcp-ns", netConfig.TCPPortsFromHost)...)
	// The same, but for the UDP ports
	pastaArgs = append(pastaArgs, portMappingArgs("--udp-ports", netConfig.UDPPortsToHost)...)
	pastaArgs = append(pastaArgs, portMappingArgs("--udp-ns", netConfig.UDPPortsFromHost)...)

	pastaArgs = append(pastaArgs, fmt.Sprintf("%d", jailedPid))

	pastaCmd := exec.Command("pasta", pastaArgs...)
	pastaCmd.SysProcAttr = &syscall.SysProcAttr{
		// Kill pasta when drop is killed.
		Pdeathsig: syscall.SIGKILL,
	}

	pastaLog := func() string {
		content, err := os.ReadFile(logPath)
		if err != nil {
			return ""
		}
		return fmt.Sprintf("\n\nPasta log:\n%s", string(content))
	}

	if err := pastaCmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("pasta binary for isolated networking not found.\n" +
				"Please install passt/pasta package, for example, on Debian/Ubuntu: \n\n" +
				"   $ sudo apt-get install passt\n\n" +
				"The package is available on most Linux distributions, see:\n" +
				"https://passt.top/passt/about/#availability")
		}
		return nil, fmt.Errorf("failed to start pasta: %v", err)
	}

	// When started as a daemon, pasta parent process exits after
	// network setup is done and pid is written to pidPath.
	if err := pastaCmd.Wait(); err != nil {
		return nil, fmt.Errorf("failed to start pasta to isolate networking%v", pastaLog())
	}

	var daemonPid int

	cleanup := func() {
		if daemonPid != 0 {
			daemon, err := os.FindProcess(daemonPid)
			if err == nil {
				daemon.Kill()
			}
		}
		os.Remove(pidPath)
	}
	daemonPid, err := readDaemonPid(pidPath)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("failed to read pasta daemon pid: %v%v", err, pastaLog())
	}

	return cleanup, nil
}

// readDaemonPid reads pasta daemon pid from a file.
func readDaemonPid(pidPath string) (int, error) {
	content, err := os.ReadFile(pidPath)
	if err != nil {
		return 0, fmt.Errorf("failed to read %s: %v", pidPath, err)
	}
	pidStr := strings.TrimSpace(string(content))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0, fmt.Errorf("failed to parse pasta pid: %v", err)
	}
	return pid, nil
}
