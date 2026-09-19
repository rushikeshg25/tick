package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/rushikeshg25/tick"
	"github.com/rushikeshg25/tick/worker/httpstore"
)

func runCoordinator(args []string) error {
	fs := flag.NewFlagSet("coordinator", flag.ExitOnError)
	addr := fs.String("addr", ":8080", "listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}

	srv := httpstore.NewServer(tick.NewSystemClock())
	log.Printf("coordinator listening on %s", *addr)
	return http.ListenAndServe(*addr, srv.Handler())
}
