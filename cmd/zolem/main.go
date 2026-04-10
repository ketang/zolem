package main

import (
	"flag"
	"log"
)

func main() {
	localAdminAddr := flag.String("local-admin-addr", "", "listen address for local admin control plane")
	localAddr := flag.String("local-addr", "", "listen address for local fixed-listener mode")
	localProvider := flag.String("local-provider", "", "provider for local fixed-listener mode")
	localProfile := flag.String("local-profile", "default", "profile name for local fixed-listener mode")
	localBackend := flag.String("local-backend", "lorem", "backend for local fixed-listener mode")
	localFixturesDir := flag.String("local-fixtures-dir", "", "fixtures directory for local runtime fixture backend")
	localTLSCert := flag.String("local-tls-cert", "", "certificate file for local admin or fixed-listener TLS")
	localTLSKey := flag.String("local-tls-key", "", "key file for local admin or fixed-listener TLS")
	flag.Parse()

	if *localAdminAddr != "" {
		if err := runLocalAdmin(localAdminOptions{
			Addr:        *localAdminAddr,
			FixturesDir: *localFixturesDir,
			TLS: localTLSConfig{
				CertFile: *localTLSCert,
				KeyFile:  *localTLSKey,
			},
		}, startupDeps{}); err != nil {
			log.Fatal(err)
		}
		return
	}

	if *localProvider != "" {
		if err := runLocal(localOptions{
			Addr:        *localAddr,
			Provider:    *localProvider,
			Profile:     *localProfile,
			Backend:     *localBackend,
			FixturesDir: *localFixturesDir,
			TLS: localTLSConfig{
				CertFile: *localTLSCert,
				KeyFile:  *localTLSKey,
			},
		}, startupDeps{}); err != nil {
			log.Fatal(err)
		}
		return
	}

	log.Fatal("choose either -local-admin-addr or -local-provider")
}
