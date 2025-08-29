package netns

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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

// waitForSocket polls for socket availability with timeout
func waitForSocket(sockPath string) error {
	const timeout = 5 * time.Second
	pollInterval := 100 * time.Millisecond

	start := time.Now()
	for time.Since(start) < timeout {
		if _, err := os.Stat(sockPath); err == nil {
			// Wait until the socket accepts connections.
			conn, err := net.Dial("unix", sockPath)
			if err == nil {
				conn.Close()
				return nil
			}
		}
		time.Sleep(pollInterval)
		pollInterval *= 2
	}
	return fmt.Errorf("timed out waiting for slirp4netns socket %s after %v", sockPath, timeout)
}

// setupPortForwarding configures port forwarding using slirp4netns API socket
func setupPortForwarding(sockPath string, portForwards []*config.PortForward) error {
	if err := waitForSocket(sockPath); err != nil {
		return err
	}

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

func randomString(length int) string {
	bytes := make([]byte, length/2)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// StartSlirp4netns starts slirp4netns to provide network connectivity
// within a network namespace and configures port forwarding.
//
// Returns a cleanup function that should be called when program exits.
func StartSlirp4netns(jailedPid int, portForwards []*config.PortForward) (func(), error) {
	var sockPath string
	var slirpArgs []string

	slirpArgs = []string{
		"--configure",
		"--mtu=65520",
		"--disable-host-loopback",
	}
	needPortForwading := len(portForwards) > 0
	if needPortForwading {
		sockPath = filepath.Join(os.TempDir(), fmt.Sprintf("dirjail-%d-%s", jailedPid, randomString(20)))
		slirpArgs = append(slirpArgs, "--api-socket", sockPath)
	}
	slirpArgs = append(slirpArgs, fmt.Sprintf("%d", jailedPid), "tap0")
	slirpCmd := exec.Command("slirp4netns", slirpArgs...)
	slirpCmd.Stderr = os.Stderr

	if err := slirpCmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start slirp4netns: %v", err)
	}

	cleanup := func() {
		slirpCmd.Process.Kill()
		slirpCmd.Wait()
		if sockPath != "" {
			os.Remove(sockPath)
		}
	}

	if needPortForwading {
		if err := setupPortForwarding(sockPath, portForwards); err != nil {
			cleanup()
			return nil, fmt.Errorf("failed to setup port forwarding: %v", err)
		}
	}

	return cleanup, nil
}
