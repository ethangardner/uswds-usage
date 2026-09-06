package app

import (
	"flag"
	"fmt"
	"net/http"
)

// runServeCmd serves a directory (the generated docs/index.html report, by
// default) over plain HTTP for local preview -- the same content GitHub
// Pages serves, just without pushing first.
func runServeCmd(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dir := fs.String("dir", "docs", "directory to serve")
	addr := fs.String("addr", ":8000", "address to listen on")
	if err := fs.Parse(args); err != nil {
		return err
	}

	fmt.Printf("Serving %s at http://localhost%s (Ctrl+C to stop)\n", *dir, *addr)
	return http.ListenAndServe(*addr, http.FileServer(http.Dir(*dir)))
}
