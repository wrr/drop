package ipc

import (
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/wrr/drop/internal/config"
	"github.com/wrr/drop/internal/jailfs"
)

func TestParentChildCommunication(t *testing.T) {
	ftmp, err := os.CreateTemp("", "drop-ipc-test")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer os.Remove(ftmp.Name())
	defer ftmp.Close()

	parentEnd, childEnd, err := NewParentChildSocket()
	if err != nil {
		t.Fatalf("NewParentChildSocket: %v", err)
	}

	sentArgs := ChildArgs{
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
				Mode: "isolated",
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
		defer childEnd.Close()
		receivedArgs, err := childEnd.RecvChildArgs()
		if err != nil {
			done <- err
			return
		}
		if !reflect.DeepEqual(receivedArgs, &sentArgs) {
			done <- fmt.Errorf("received args differ from sent args:\ngot:  %+v\nwant: %+v", receivedArgs, &sentArgs)
			return
		}

		done <- nil
	}()

	if err := parentEnd.SendChildArgs(sentArgs); err != nil {
		t.Fatalf("SendChildArgs: %v", err)
	}

	if err := parentEnd.Close(); err != nil {
		t.Fatalf("parent Close: %v", err)
	}

	if err := <-done; err != nil {
		t.Fatalf("child goroutine: %v", err)
	}
}
