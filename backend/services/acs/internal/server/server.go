package server

import (
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

	log.Printf("ACS running at %s%s", c.Port, c.Route)

	err := http.ListenAndServe(c.Port, nil)
	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}
}
