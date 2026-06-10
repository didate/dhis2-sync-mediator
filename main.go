package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// OpenHIMResponse is the structured response OpenHIM expects from a mediator.
// It contains the actual response to the client + orchestration metadata
// that will appear in the transaction log.
type OpenHIMResponse struct {
	XMediatorURN   string            `json:"x-mediator-urn"`
	Status         string            `json:"status"` // Successful / Failed / Completed / etc.
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

	// Register with OpenHIM
	ohc := NewOpenHIMClient(cfg)
	if err := ohc.Register(); err != nil {
		log.Fatalf("OpenHIM registration failed: %v", err)
	}
	ohc.Heartbeat()

	// HTTP server
	http.HandleFunc("/sync", handleSync)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	addr := ":" + cfg.MediatorPort
	log.Printf("Mediator listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

func handleSync(w http.ResponseWriter, r *http.Request) {
	cfg := LoadConfig()
	log.Printf("Received %s %s", r.Method, r.URL.String())

	dataSet := r.URL.Query().Get("dataSet")
	orgUnit := r.URL.Query().Get("orgUnit")
	period := r.URL.Query().Get("period")

	if dataSet == "" || orgUnit == "" || period == "" {
		respondError(w, http.StatusBadRequest,
			"Missing required query params: dataSet, orgUnit, period")
		return
	}

	src := NewDHIS2Client(cfg.DHIS2SourceURL, cfg.DHIS2SourcePAT)
	startSrc := time.Now()
	dvs, rawBody, srcEndpoint, err := src.FetchDataValueSet(dataSet, orgUnit, period)
	endSrc := time.Now()

	if err != nil {
		log.Printf("Source fetch error: %v", err)
		respondError(w, http.StatusBadGateway,
			fmt.Sprintf("Source fetch failed: %v", err))
		return
	}

	log.Printf("Fetched %d dataValues from source in %v",
		len(dvs.DataValues), endSrc.Sub(startSrc))

	// Step 2: Convert to FHIR MeasureReport
	measureReport, err := DataValueSetToMeasureReport(dvs, cfg.DHIS2SourceURL)
	if err != nil {
		log.Printf("FHIR conversion error: %v", err)
		respondError(w, http.StatusInternalServerError,
			fmt.Sprintf("FHIR conversion failed: %v", err))
		return
	}
	endConvert := time.Now()

	fhirBody, _ := json.MarshalIndent(measureReport, "", "  ")
	log.Printf("Converted to FHIR MeasureReport (%d groups) in %v",
		len(measureReport.Group), endConvert.Sub(endSrc))

	// Step 3: Convert FHIR MeasureReport back to DataValueSet
	targetDVS, err := MeasureReportToDataValueSet(measureReport)
	if err != nil {
		log.Printf("FHIR to DataValueSet conversion error: %v", err)
		respondError(w, http.StatusInternalServerError,
			fmt.Sprintf("FHIR reverse conversion failed: %v", err))
		return
	}
	// Preserve original period from source (FHIR period is date-based, DHIS2 needs the original format)
	targetDVS.Period = dvs.Period
	targetDVS.CompleteDate = truncateToDate(dvs.CompleteDate)
	targetDVS.AttributeOptionCombo = dvs.AttributeOptionCombo

	// Step 4: Push to target DHIS2
	target := NewDHIS2Client(cfg.DHIS2TargetURL, cfg.DHIS2TargetPAT)
	startTarget := time.Now()
	targetRawBody, targetEndpoint, err := target.PostDataValueSet(targetDVS)
	endTarget := time.Now()

	targetBody, _ := json.Marshal(targetDVS)
	pushStatus := "Successful"
	pushHTTPStatus := 200

	if err != nil {
		log.Printf("Target push error: %v", err)
		pushStatus = "Failed"
		pushHTTPStatus = 502
	} else {
		log.Printf("Pushed %d dataValues to target in %v",
			len(targetDVS.DataValues), endTarget.Sub(startTarget))
	}

	respBody, _ := json.MarshalIndent(map[string]interface{}{
		"step":           "4",
		"action":         "push-to-target",
		"dataValueCount": len(targetDVS.DataValues),
		"status":         pushStatus,
	}, "", "  ")

	openhimResp := OpenHIMResponse{
		XMediatorURN: cfg.MediatorURN,
		Status:       pushStatus,
		Response: OHResponse{
			Status:    pushHTTPStatus,
			Headers:   map[string]string{"Content-Type": "application/json"},
			Body:      string(respBody),
			Timestamp: endTarget,
		},
		Orchestrations: []Orchestration{
			{
				Name: "pull-dhis2-source",
				Request: OHRequest{
					Path:      srcEndpoint,
					Method:    "GET",
					Headers:   map[string]string{"Authorization": "ApiToken ***"},
					Timestamp: startSrc,
				},
				Response: OHResponse{
					Status:    200,
					Headers:   map[string]string{"Content-Type": "application/json"},
					Body:      string(rawBody),
					Timestamp: endSrc,
				},
			},
			{
				Name: "convert-to-fhir-measurereport",
				Request: OHRequest{
					Path:      "internal://fhir-conversion",
					Method:    "TRANSFORM",
					Timestamp: endSrc,
				},
				Response: OHResponse{
					Status:    200,
					Headers:   map[string]string{"Content-Type": "application/fhir+json"},
					Body:      string(fhirBody),
					Timestamp: endConvert,
				},
			},
			{
				Name: "push-dhis2-target",
				Request: OHRequest{
					Path:      targetEndpoint,
					Method:    "POST",
					Headers:   map[string]string{"Authorization": "ApiToken ***"},
					Body:      string(targetBody),
					Timestamp: startTarget,
				},
				Response: OHResponse{
					Status:    pushHTTPStatus,
					Headers:   map[string]string{"Content-Type": "application/json"},
					Body:      string(targetRawBody),
					Timestamp: endTarget,
				},
			},
		},
	}

	w.Header().Set("Content-Type", "application/json+openhim")
	json.NewEncoder(w).Encode(openhimResp)
}

// truncateToDate takes a DHIS2 date/datetime string and returns just the date part (YYYY-MM-DD).
func truncateToDate(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

func respondError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json+openhim")
	json.NewEncoder(w).Encode(OpenHIMResponse{
		XMediatorURN: "urn:mediator:dhis2-sync",
		Status:       "Failed",
		Response: OHResponse{
			Status:    status,
			Headers:   map[string]string{"Content-Type": "application/json"},
			Body:      fmt.Sprintf(`{"error":%q}`, message),
			Timestamp: time.Now(),
		},
	})
}
