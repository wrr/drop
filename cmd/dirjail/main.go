package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
)

func childProcessEntry() {
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "Error:", err)
	os.Exit(1)
}

func main() {

	if len(os.Args) > 1 && os.Args[1] == "-child" {
		fmt.Println("Child started")
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
		die(err)
	}

	cmd := exec.Command(os.Args[0], "-child")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		die(fmt.Errorf("failed to start jailed child process: %v", err))
	}
}
