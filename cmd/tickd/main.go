// Command tickd runs a tick cluster so that faults can be injected into a
// real one.
//
// Three modes:
//
//	tickd coordinator            holds the node ID claims
//	tickd generator              leases a node ID and mints IDs
//	tickd verify                 drains every generator and checks uniqueness
//
// The point of the verifier is that it is the only component able to see a
// duplicate. Each generator only knows its own output; uniqueness is a
// property of the cluster.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "coordinator":
		err = runCoordinator(os.Args[2:])
	case "generator":
		err = runGenerator(os.Args[2:])
	case "verify":
		err = runVerify(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "tickd: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: tickd <coordinator|generator|verify> [flags]

  coordinator   hold node ID claims over HTTP
  generator     lease a node ID and mint IDs continuously
  verify        drain every generator and check global uniqueness
`)
}
