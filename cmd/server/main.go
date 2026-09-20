package main

import (
	"errors"
	"flag"
	"log"
	"net/http"
	"os"

	"metrologylab/internal/api"
	"metrologylab/internal/store"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:5199", "HTTP listen address")
	data := flag.String("data", "./data", "persistent data directory")
	flag.Parse()
	st, err := store.Open(*data)
	if err != nil {
		log.Fatalf("open storage: %v", err)
	}
	srv := &http.Server{Addr: *listen, Handler: api.New(st, nil)}
	log.Printf("metrology lab listening on http://%s", *listen)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
		os.Exit(1)
	}
}
