package main

import (
	"log"
	"net/http"

	"github.com/didate/dhis2-sync-mediator/internal/config"
	"github.com/didate/dhis2-sync-mediator/internal/handler"
	"github.com/didate/dhis2-sync-mediator/internal/openhim"
)

func main() {
	cfg := config.LoadConfig()

	ohc := openhim.NewOpenHIMClient(cfg)
	if err := ohc.Register(); err != nil {
		log.Fatalf("OpenHIM registration failed: %v", err)
	}
	ohc.Heartbeat()

	http.HandleFunc("/pull-orgunit", func(w http.ResponseWriter, r *http.Request) {
		handler.HandlePullOrgUnit(w, r, cfg, ohc)
	})
	http.HandleFunc("/dhis2-to-fhir", func(w http.ResponseWriter, r *http.Request) {
		handler.HandleDHIS2ToFHIR(w, r, cfg, ohc)
	})
	http.HandleFunc("/fhir-to-dhis2", func(w http.ResponseWriter, r *http.Request) {
		handler.HandleFHIRToDHIS2(w, r, cfg, ohc)
	})
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	addr := ":" + cfg.MediatorPort
	log.Printf("Mediator listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
