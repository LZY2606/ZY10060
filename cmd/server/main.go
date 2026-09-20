package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"metrolab/internal/httpapi"
	"metrolab/internal/service"
	"metrolab/internal/store"
	"metrolab/internal/web"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:5199", "listen address")
	dataDir := flag.String("data", envOr("METROLAB_DATA", "data"), "data directory")
	flag.Parse()

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		log.Fatalf("data dir: %v", err)
	}
	abs, err := filepath.Abs(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	st, recs, err := store.Open(abs)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	svc, err := service.New(st, recs)
	if err != nil {
		log.Fatalf("restore service: %v", err)
	}
	srv := httpapi.New(svc, web.Handler())
	log.Printf("metrolab listening on http://%s (data=%s)", *listen, abs)
	if err := http.ListenAndServe(*listen, srv.Routes()); err != nil {
		log.Fatal(err)
	}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
