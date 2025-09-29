package netns

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wrr/drop/internal/config"
)

// StartPasta starts pasta to provide network connectivity
// within a network namespace and configures port forwarding.
//
// Returns a cleanup function that should be called when program exits.
func StartPasta(jailedPid int, portForwards []*config.PortForward, runDir string) (func(), error) {
	var pastaArgs []string

	// Named pipe to be used by pasta to notify when network setup is
	// finished.
	pidFifoPath := filepath.Join(runDir, "pasta.pid")
	err := syscall.Mkfifo(pidFifoPath, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to create pipe %s: pidFifoPath, %v", pidFifoPath, err)
	}

	pastaArgs = []string{
		"--config-net",
		// Address to be used in the namespace as DNS. Pasta forwards DNS
		// requests to this address to the actual host DNS.
		"--dns-forward", "10.0.2.3",
		"--pid", pidFifoPath,
		"--udp-ports", "none",
		// Ports open on the host that are accessible from a namespace.
		// This mapping is also needed to allow drop instances to connect
		// to one another (one instance exposes a port to host with
		// --tcp-port and the other needs --tcp-ns to be able connect to
		// this port).
		"--tcp-ns", "none",
		"--udp-ns", "none",
		"--no-map-gw",
		"--log-file", filepath.Join(runDir, "pasta.log"),
	}

	// Ports open in the namespace that are accessible from the host
	tcpPorts := []string{"auto"}
	// Add port forwarding arguments
	if len(portForwards) > 0 {
		tcpPorts = make([]string, 0, len(portForwards))
		for _, pf := range portForwards {
			hostAddr := pf.HostIP
			if hostAddr == "" {
				hostAddr = "localhost"
			}
			fmt.Printf("Port forwarding: %s:%d -> %d\n", hostAddr, pf.HostPort, pf.GuestPort)

			if pf.HostIP == "" {
				tcpPorts = append(tcpPorts, fmt.Sprintf("%d:%d", pf.HostPort, pf.GuestPort))
			} else {
				tcpPorts = append(tcpPorts, fmt.Sprintf("%s/%d:%d", pf.HostIP, pf.HostPort, pf.GuestPort))
			}
		}
	}

	if len(tcpPorts) > 0 {
		pastaArgs = append(pastaArgs, "--tcp-port", strings.Join(tcpPorts, ","))
	}

	pastaArgs = append(pastaArgs, fmt.Sprintf("%d", jailedPid))

	pastaCmd := exec.Command("pasta", pastaArgs...)
	// pastaCmd.Stderr = os.Stderr
	pastaCmd.SysProcAttr = &syscall.SysProcAttr{
		// Kill pasta when drop is killed.
		Pdeathsig: syscall.SIGKILL,
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

	cleanup := func() {
		pastaCmd.Process.Kill()
		pastaCmd.Wait()
		os.Remove(pidFifoPath)
	}

	if err := waitNetworkReady(pidFifoPath); err != nil {
		cleanup()
		return nil, fmt.Errorf("pasta failed to start: %v", err)
	}

	return cleanup, nil
}

// waitNetworkReady waits for pasta to write its PID to the named
// pipe, which indicates network configuration is done.
func waitNetworkReady(pidFifoPath string) error {
	const timeout = 5 * time.Second

	// TODO: this blocks indefinitely if pasta didn't open its end for writing (for
	// example due to initialization error).
	file, err := os.Open(pidFifoPath)
	if err != nil {
		return fmt.Errorf("failed to open %s: %v", pidFifoPath, err)
	}
	defer file.Close()
	file.SetReadDeadline(time.Now().Add(timeout))

	buf := make([]byte, 32)
	if _, err := file.Read(buf); err != nil {
		return fmt.Errorf("failed to read from %s: %v", pidFifoPath, err)
	}
	// pidContent := strings.TrimSpace(string(buf[:n]))
	// fmt.Printf("DEBUG: pasta PID: %s\n", pidContent)
	return nil
}
