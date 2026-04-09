package main

import (
	"flag"
	"log"
)

func main() {
	cfgPath := flag.String("config", "zolem.yaml", "path to config file")
	localAdminAddr := flag.String("local-admin-addr", "", "listen address for local admin control plane")
	localAddr := flag.String("local-addr", "", "listen address for local fixed-listener mode")
	localProvider := flag.String("local-provider", "", "provider for local fixed-listener mode")
	localProfile := flag.String("local-profile", "default", "profile name for local fixed-listener mode")
	localBackend := flag.String("local-backend", "lorem", "backend for local fixed-listener mode")
	flag.Parse()

	if *localAdminAddr != "" {
		if err := runLocalAdmin(localAdminOptions{Addr: *localAdminAddr}, startupDeps{}); err != nil {
			log.Fatal(err)
		}
		return
	}

	if *localProvider != "" {
		if err := runLocal(localOptions{
			Addr:     *localAddr,
			Provider: *localProvider,
			Profile:  *localProfile,
			Backend:  *localBackend,
		}, startupDeps{}); err != nil {
			log.Fatal(err)
		}
		return
	}

	if err := run(*cfgPath, startupDeps{}); err != nil {
		log.Fatal(err)
	}
}
