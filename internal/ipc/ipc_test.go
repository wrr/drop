package ipc

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/wrr/drop/internal/config"
	"github.com/wrr/drop/internal/jailfs"
)

func TestSandboxArgsSending(t *testing.T) {
	sender, receiver, err := NewArgsSocket()
	if err != nil {
		t.Fatalf("NewArgsSocket: %v", err)
	}

	sentArgs := SandboxArgs{
		EnvId: "test-env",
		Paths: &jailfs.Paths{
			Cwd:       "/home/alice/project",
			DropHome:  "/home/alice/.local/share/drop",
			Env:       "/home/alice/.local/share/drop/envs/test-env",
			FsRoot:    "/home/alice/.local/share/drop/internal/run/test-env-123/root",
			HostHome:  "/home/alice",
			Home:      "/home/alice/.local/share/drop/envs/test-env/home",
			Etc:       "/home/alice/.local/share/drop/envs/test-env/etc",
			Var:       "/home/alice/.local/share/drop/envs/test-env/var",
			Tmp:       "/tmp/drop-test-env-456",
			Run:       "/home/alice/.local/share/drop/internal/run/test-env-123",
			EmptyDir:  "/home/alice/.local/share/drop/internal/emptyd",
			EmptyFile: "/home/alice/.local/share/drop/internal/empty",
		},
		Config: &config.Config{
			Extends: "base.toml",
			Mounts: []config.Mount{
				{Source: "~/docs", Target: "~/docs", RW: false, Overlay: true},
				{Source: "~/projects", Target: "~/projects", RW: true, Overlay: false},
			},
			BlockedPaths: []string{"/root", "/mnt"},
			Environ: config.Environ{
				ExposedVars: []string{"PATH", "HOME", "TERM"},
				SetVars:     []config.EnvVar{{Name: "FOO", Value: "bar"}},
			},
			Net: config.Net{
				Mode: config.NetModeIsolated,
				TCPPublishedPorts: []config.PublishedPort{
					{HostPort: 8080, GuestPort: 3000},
					{HostPort: 8082, GuestPort: 3003},
				},
			},
		},
		ExecArgs: []string{"ls", "-al"},
	}

	done := make(chan error, 1)
	go func() {
		defer receiver.Close()
		receivedArgs, err := receiver.Recv()
		if err != nil {
			done <- err
			return
		}
		if !reflect.DeepEqual(receivedArgs, &sentArgs) {
			done <- fmt.Errorf("received args:\ngot:  %+v\nwant: %+v", receivedArgs, &sentArgs)
			return
		}

		done <- nil
	}()

	if err := sender.Send(sentArgs); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if err := sender.Close(); err != nil {
		t.Fatalf("sender end Close: %v", err)
	}

	if err := <-done; err != nil {
		t.Fatalf("child goroutine: %v", err)
	}
}

func TestToggleSerialization(t *testing.T) {
	// Regression test, firewall.Enabled=false state was lost during
	// serialization when a pointer to bool was used to represent its 3
	// distinct states.
	for _, toggle := range []config.Toggle{config.ToggleUnset, config.ToggleOn, config.ToggleOff} {
		sender, receiver, err := NewArgsSocket()
		if err != nil {
			t.Fatalf("NewArgsSocket: %v", err)
		}
		defer sender.Close()
		defer receiver.Close()

		cfg := &config.Config{
			Net: config.Net{
				Firewall: &config.Firewall{Enabled: toggle, DefaultAction: "deny"},
			},
		}
		if err := sender.Send(SandboxArgs{Config: cfg}); err != nil {
			t.Fatalf("Send: %v", err)
		}
		received, err := receiver.Recv()
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		if got := received.Config.Net.Firewall.Enabled; got != toggle {
			t.Errorf("received toggle %d, want %d", got, toggle)
		}
	}
}
