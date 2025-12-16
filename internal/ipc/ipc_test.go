package ipc

import (
	"os"
	"testing"
)

func TestParentChildCommunication(t *testing.T) {
	ftmp, err := os.CreateTemp("", "drop-ipc-test")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer os.Remove(ftmp.Name())
	defer ftmp.Close()
	fcontent := "hello from child"

	parentEnd, childEnd, err := NewParentChildSocket()
	if err != nil {
		t.Fatalf("NewParentChildSocket: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		if err := childEnd.WaitNetworkReady(); err != nil {
			done <- err
			return
		}

		if _, err := ftmp.WriteString(fcontent); err != nil {
			done <- err
			return
		}
		if _, err := ftmp.Seek(0, 0); err != nil {
			done <- err
			return
		}

		if err := childEnd.SendPty(ftmp); err != nil {
			done <- err
			return
		}
		done <- childEnd.Close()
	}()

	if err := parentEnd.NotifyNetworkReady(); err != nil {
		t.Fatalf("NotifyNetworkReady: %v", err)
	}

	received, err := parentEnd.RecvPty()
	if err != nil {
		t.Fatalf("RecvPty: %v", err)
	}
	defer received.Close()

	buf := make([]byte, 100)
	n, err := received.Read(buf)
	if err != nil {
		t.Fatalf("Read from received fd: %v", err)
	}
	if got := string(buf[:n]); got != fcontent {
		t.Fatalf("got %q, want %q", got, fcontent)
	}

	if err := parentEnd.Close(); err != nil {
		t.Fatalf("parent Close: %v", err)
	}

	if err := <-done; err != nil {
		t.Fatalf("child goroutine: %v", err)
	}
}
