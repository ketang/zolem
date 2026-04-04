package main

import (
	"flag"
	"log"
)

func main() {
	cfgPath := flag.String("config", "zolem.yaml", "path to config file")
	flag.Parse()

	if err := run(*cfgPath, startupDeps{}); err != nil {
		log.Fatal(err)
	}
}
