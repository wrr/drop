package main

import (
	"flag"
	"fmt"
	"os"
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

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "dirjail limits programs abilities to read and write user's files\nOptions:\n")
		flag.PrintDefaults()
	}

	err := flag.CommandLine.Parse(os.Args[1:])
	if err != nil {
		die(err)
	}

}
