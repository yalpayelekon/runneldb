package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/yalpayelekon/runneldb"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: runneldb serve [--path data.wal] [--addr :7070]")
		os.Exit(2)
	}
	flags := flag.NewFlagSet("serve", flag.ExitOnError)
	path := flags.String("path", "runneldb.wal", "database WAL path")
	addr := flags.String("addr", ":7070", "HTTP listen address")
	_ = flags.Parse(os.Args[2:])

	db, err := runneldb.Open(*path)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	log.Printf("RunnelDB listening on %s (data: %s)", *addr, *path)
	log.Fatal(http.ListenAndServe(*addr, runneldb.Handler(db)))
}
