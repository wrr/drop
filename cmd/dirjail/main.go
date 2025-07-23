package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func childProcessEntry() {
	fmt.Println("Child started")

	// TODO: use SHELL env variable
	pname := "bash"
	cmd := exec.Command(pname)

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		die("%s failed: %v", pname, err)
	}
	// Ignore errors (bash exits with an error if last executed command
	// exited with an error)
	cmd.Wait()
}

func die(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	os.Exit(1)
}

func main() {

	if len(os.Args) > 1 && os.Args[1] == "-child" {
		childProcessEntry()
		os.Exit(0)
	}
	fmt.Println("Parent started")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "dirjail limits programs abilities to read and write user's files\nOptions:\n")
		flag.PrintDefaults()
	}

	err := flag.CommandLine.Parse(os.Args[1:])
	if err != nil {
		die("failed to parse command line: %v", err)
	}

	cmd := exec.Command(os.Args[0], "-child")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWNS |
			syscall.CLONE_NEWIPC |
			syscall.CLONE_NEWPID |
			syscall.CLONE_NEWUSER,
		// Keep using current user uid an gid in the jail (so the user is
		// recognized as the same user)
		UidMappings: []syscall.SysProcIDMap{
			{
				ContainerID: os.Getuid(),
				HostID:      os.Getuid(),
				Size:        1,
			},
		},
		GidMappings: []syscall.SysProcIDMap{
			{
				ContainerID: os.Getgid(),
				HostID:      os.Getgid(),
				Size:        1,
			},
		},
		// TODO: disalow changing of the group memership
	}
	if err := cmd.Start(); err != nil {
		die("failed to start jailed child process: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		die("jailed process exited with error: %v", err)
	}
}
