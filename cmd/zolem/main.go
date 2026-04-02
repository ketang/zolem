// cmd/zolem/main.go
package main

import (
	"flag"
	"log"
	"net/http"

	"zolem.dev/zolem/internal/config"
)

func main() {
	cfgPath := flag.String("config", "zolem.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	log.Printf("zolem listening on %s", cfg.Server.Addr)
	if err := http.ListenAndServe(cfg.Server.Addr, nil); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
