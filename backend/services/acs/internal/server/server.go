package server

import (
	"crypto/tls"
	"log"
	"net/http"
	"oktopUSP/backend/services/acs/internal/auth"
	"oktopUSP/backend/services/acs/internal/config"
	"oktopUSP/backend/services/acs/internal/nats"
	"oktopUSP/backend/services/acs/internal/server/handler"
	"os"
)

func Run(c config.Acs, natsActions nats.NatsActions, h *handler.Handler) {
	log.Printf("ACS Digest Authentication enabled: username=%s password=%s", c.Username, c.Password)
	
	if c.Username != "" && c.Password != "" {
		log.Println("ACS Digest Authentication enabled")
		http.HandleFunc(c.Route, auth.DigestAuthWrap(h.CwmpHandler, c.Username, c.Password))
	} else {
		log.Println("WARNING: ACS running without authentication")
		http.HandleFunc(c.Route, h.CwmpHandler)
	}
	go h.HandleCpeStatus()

	log.Printf("ACS KeepAliveInterval: %s", c.KeepAliveInterval)
	log.Printf("ACS EnableTls: %v", c.EnableTls)
	log.Printf("ACS TlsCert: %s TlsKey: %s", c.TlsCert, c.TlsKey)
	if c.EnableTls {
		if c.TlsCert == "" || c.TlsKey == "" {
				log.Fatal("ACS_ENABLE_TLS is true but ACS_TLS_CERT or ACS_TLS_KEY is not set")
		}
		// Start HTTP on separate goroutine if both HTTP and HTTPS are needed
		go func() {
			log.Printf("ACS HTTP running at %s%s", c.Port, c.Route)
			if err := http.ListenAndServe(c.Port, nil); err != nil {
					log.Printf("HTTP server error: %v", err)
			}
		}()
		log.Printf("ACS HTTPS running at %s%s (cert=%s, key=%s)", c.TlsPort, c.Route, c.TlsCert, c.TlsKey)
		tlsServer := &http.Server{
			Addr: c.TlsPort,
			TLSConfig: &tls.Config{
				MaxVersion: tls.VersionTLS12,
			},
		}
		err := tlsServer.ListenAndServeTLS(c.TlsCert, c.TlsKey)
		if err != nil {
			log.Fatal(err)
			os.Exit(1)
		}
	} else {
		log.Printf("ACS HTTP running at %s%s", c.Port, c.Route)
		err := http.ListenAndServe(c.Port, nil)
		if err != nil {
				log.Fatal(err)
				os.Exit(1)
		}
	}
}
