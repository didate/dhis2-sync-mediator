package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// OpenHIMResponse is the structured response OpenHIM expects from a mediator.
type OpenHIMResponse struct {
	XMediatorURN   string            `json:"x-mediator-urn"`
	Status         string            `json:"status"`
	Response       OHResponse        `json:"response"`
	Orchestrations []Orchestration   `json:"orchestrations"`
	Properties     map[string]string `json:"properties,omitempty"`
}

type OHResponse struct {
	Status    int               `json:"status"`
	Headers   map[string]string `json:"headers"`
	Body      string            `json:"body"`
	Timestamp time.Time         `json:"timestamp"`
}

type Orchestration struct {
	Name     string     `json:"name"`
	Request  OHRequest  `json:"request"`
	Response OHResponse `json:"response"`
}

type OHRequest struct {
	Path        string            `json:"path"`
	Headers     map[string]string `json:"headers"`
	Querystring string            `json:"querystring,omitempty"`
	Body        string            `json:"body,omitempty"`
	Method      string            `json:"method"`
	Timestamp   time.Time         `json:"timestamp"`
}

func main() {
	cfg := LoadConfig()

	ohc := NewOpenHIMClient(cfg)
	if err := ohc.Register(); err != nil {
		log.Fatalf("OpenHIM registration failed: %v", err)
	}
	ohc.Heartbeat()

	http.HandleFunc("/pull-orgunit", func(w http.ResponseWriter, r *http.Request) {
		handlePullOrgUnit(w, r, cfg, ohc)
	})
	http.HandleFunc("/dhis2-to-fhir", func(w http.ResponseWriter, r *http.Request) {
		handleDHIS2ToFHIR(w, r, cfg, ohc)
	})
	http.HandleFunc("/fhir-to-dhis2", func(w http.ResponseWriter, r *http.Request) {
		handleFHIRToDHIS2(w, r, cfg, ohc)
	})
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	addr := ":" + cfg.MediatorPort
	log.Printf("Mediator listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

// respondAccepted sends an immediate 202 response so OpenHIM doesn't timeout.
// The actual work runs in a goroutine that updates the transaction when done.
func respondAccepted(w http.ResponseWriter, mediatorURN, message string) {
	w.Header().Set("Content-Type", "application/json+openhim")
	json.NewEncoder(w).Encode(OpenHIMResponse{
		XMediatorURN: mediatorURN,
		Status:       "Processing",
		Response: OHResponse{
			Status:    202,
			Headers:   map[string]string{"Content-Type": "application/json"},
			Body:      fmt.Sprintf(`{"message":%q}`, message),
			Timestamp: time.Now(),
		},
	})
}

func respondError(w http.ResponseWriter, mediatorURN string, status int, message string) {
	w.Header().Set("Content-Type", "application/json+openhim")
	json.NewEncoder(w).Encode(OpenHIMResponse{
		XMediatorURN: mediatorURN,
		Status:       "Failed",
		Response: OHResponse{
			Status:    status,
			Headers:   map[string]string{"Content-Type": "application/json"},
			Body:      fmt.Sprintf(`{"error":%q}`, message),
			Timestamp: time.Now(),
		},
	})
}

func truncateToDate(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}
