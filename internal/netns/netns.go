package netns

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/wrr/dirjail/internal/config"
)

type SlirpCommand struct {
	Execute   string         `json:"execute"`
	Arguments map[string]any `json:"arguments"`
}

// SetupFirewall restrict traffic to the host loopback which could be
// exposed on machines where DNS IP address on the host is a loopback.
//
// Generic loopback traffic restriction is provided by the
// --disable-host-loopback option to slirp4netns, iptables rules are
// only needed to cover the one missing case.
//
// For details, see:
// https://github.com/rootless-containers/slirp4netns/blob/v0.4.3/slirp4netns.1.md#filtering-connections
func SetupFirewall() error {
	// Accept UDP port 53 to 10.0.2.3
	accept := exec.Command("iptables", "-A", "OUTPUT", "-d", "10.0.2.3", "-p", "udp", "--dport", "53", "-j", "ACCEPT")
	if err := accept.Run(); err != nil {
		return fmt.Errorf("failed to run 'iptables' to accept traffic to DNS port: %w", err)
	}

	// Drop all other traffic to 10.0.2.3
	drop := exec.Command("iptables", "-A", "OUTPUT", "-d", "10.0.2.3", "-j", "DROP")
	if err := drop.Run(); err != nil {
		return fmt.Errorf("failed to run 'iptables' to drop traffic to DNS IP: %w", err)
	}

	return nil
}

// StartSlirp4netns starts slirp4netns to provide network connectivity
// within a network namespace and configures port forwarding.
//
// Returns a cleanup function that should be called when program exits.
func StartSlirp4netns(jailedPid int, portForwards []*config.PortForward, runDir string) (func(), error) {
	var sockPath string
	var slirpArgs []string

	// Create pipe for ready notification
	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create pipe: %v", err)
	}

	slirpArgs = []string{
		"--configure",
		"--mtu=65520",
		"--disable-host-loopback",
		"--ready-fd=3",
	}
	needPortForwading := len(portForwards) > 0
	if needPortForwading {
		sockPath = filepath.Join(runDir, "slirp4netns.sock")
		slirpArgs = append(slirpArgs, "--api-socket", sockPath)
	}
	slirpArgs = append(slirpArgs, fmt.Sprintf("%d", jailedPid), "tap0")
	slirpCmd := exec.Command("slirp4netns", slirpArgs...)
	slirpCmd.Stderr = os.Stderr
	slirpCmd.ExtraFiles = []*os.File{readyWrite}

	if err := slirpCmd.Start(); err != nil {
		readyRead.Close()
		readyWrite.Close()
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("slirp4netns binary for isolated networking not found.\n" +
				"Please install slirp4netns package, for example, on Debian/Ubuntu: \n\n" +
				"   $ sudo apt-get install slirp4netns\n\n" +
				"The package is available on most Linux distributions, see:\n" +
				"https://github.com/rootless-containers/slirp4netns?tab=readme-ov-file#quick-start")
		}
		return nil, fmt.Errorf("failed to start slirp4netns: %v", err)
	}

	cleanup := func() {
		slirpCmd.Process.Kill()
		slirpCmd.Wait()
		if sockPath != "" {
			os.Remove(sockPath)
		}
	}

	// Close write end in parent
	readyWrite.Close()
	err = waitForReady(readyRead)
	readyRead.Close()
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("slirp4netns failed to start: %v", err)
	}

	if needPortForwading {
		if err := setupPortForwarding(sockPath, portForwards); err != nil {
			cleanup()
			return nil, fmt.Errorf("failed to setup port forwarding: %v", err)
		}
	}

	return cleanup, nil
}

// waitForReady waits for slirp4netns to signal readiness via readyRead file.
func waitForReady(readyRead *os.File) error {
	const timeout = 5 * time.Second
	readyRead.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 1)
	n, err := readyRead.Read(buf)
	if err != nil {
		return fmt.Errorf("failed to read from ready-fd: %v", err)
	}
	if n != 1 || buf[0] != '1' {
		return fmt.Errorf("unexpected ready signal: got %d bytes, expected '1'", n)
	}
	return nil
}

// setupPortForwarding configures port forwarding using slirp4netns API socket
func setupPortForwarding(sockPath string, portForwards []*config.PortForward) error {
	for _, pf := range portForwards {
		cmd := SlirpCommand{
			Execute: "add_hostfwd",
			Arguments: map[string]any{
				"proto":      "tcp",
				"host_addr":  pf.HostIP,
				"host_port":  pf.HostPort,
				"guest_port": pf.GuestPort,
			},
		}
		jsonData, err := json.Marshal(cmd)
		if err != nil {
			return fmt.Errorf("failed to marshal JSON for %s:%d->%d: %v", pf.HostIP, pf.HostPort, pf.GuestPort, err)
		}

		// slirp4netns requires separate connection per each command.
		conn, err := net.Dial("unix", sockPath)
		if err != nil {
			return fmt.Errorf("failed to connect to slirp4netns socket %s: %v", sockPath, err)
		}
		_, err = conn.Write(jsonData)
		conn.Close()

		if err != nil {
			return fmt.Errorf("failed to send port forwarding command %s:%d->%d: %v", pf.HostIP, pf.HostPort, pf.GuestPort, err)
		}

		fmt.Printf("Port forwarding: %s:%d -> 10.0.2.100:%d\n", pf.HostIP, pf.HostPort, pf.GuestPort)
	}

	return nil
}
