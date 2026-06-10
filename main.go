package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
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
	http.HandleFunc("/sync", func(w http.ResponseWriter, r *http.Request) {
		handleSync(w, r, cfg)
	})
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	addr := ":" + cfg.MediatorPort
	log.Printf("Mediator listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

func handleSync(w http.ResponseWriter, r *http.Request, cfg *Config) {
	log.Printf("Received %s %s", r.Method, r.URL.String())
	startTotal := time.Now()

	dataSet := r.URL.Query().Get("dataSet")
	if dataSet == "" {
		respondError(w, http.StatusBadRequest, "Missing required query param: dataSet")
		return
	}

	src := NewDHIS2Client(cfg.DHIS2SourceURL, cfg.DHIS2SourcePAT)
	target := NewDHIS2Client(cfg.DHIS2TargetURL, cfg.DHIS2TargetPAT)
	orchestrations := []Orchestration{}

	// --- Determine org units ---
	var orgUnits []OrgUnit
	if ouParam := r.URL.Query().Get("orgUnit"); ouParam != "" {
		orgUnits = []OrgUnit{{ID: ouParam, Name: ouParam}}
	} else {
		ouLevel := cfg.DefaultOULevel
		if v := r.URL.Query().Get("ouLevel"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				ouLevel = n
			}
		}

		startOU := time.Now()
		var err error
		orgUnits, err = src.FetchOrgUnits(ouLevel)
		endOU := time.Now()

		if err != nil {
			log.Printf("Fetch org units error: %v", err)
			respondError(w, http.StatusBadGateway,
				fmt.Sprintf("Failed to fetch org units: %v", err))
			return
		}

		orchestrations = append(orchestrations, Orchestration{
			Name: "fetch-org-units",
			Request: OHRequest{
				Path:      fmt.Sprintf("%s/api/organisationUnits?level=%d", cfg.DHIS2SourceURL, ouLevel),
				Method:    "GET",
				Headers:   map[string]string{"Authorization": "ApiToken ***"},
				Timestamp: startOU,
			},
			Response: OHResponse{
				Status:    200,
				Headers:   map[string]string{"Content-Type": "application/json"},
				Body:      fmt.Sprintf(`{"count":%d}`, len(orgUnits)),
				Timestamp: endOU,
			},
		})
		log.Printf("Fetched %d org units at level %d", len(orgUnits), ouLevel)
	}

	// --- Determine periods ---
	var periods []string
	if pParam := r.URL.Query().Get("period"); pParam != "" {
		periods = []string{pParam}
	} else {
		weeks := cfg.DefaultWeeks
		if v := r.URL.Query().Get("weeks"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				weeks = n
			}
		}
		periods = GenerateWeekPeriods(weeks)
	}

	totalCombinations := len(orgUnits) * len(periods)
	log.Printf("Starting sync: %d OUs × %d periods = %d combinations",
		len(orgUnits), len(periods), totalCombinations)

	// --- Step 1: Pull all + FHIR conversion ---
	startPull := time.Now()
	pullResults := PullAll(src, dataSet, orgUnits, periods, cfg.DHIS2SourceURL, cfg.MaxWorkers)
	endPull := time.Now()

	pullSuccess := 0
	pullFail := 0
	pullEmpty := 0
	totalSourceValues := 0
	for _, r := range pullResults {
		if r.Error != nil {
			pullFail++
		} else if r.TargetDVS == nil || len(r.TargetDVS.DataValues) == 0 {
			pullEmpty++
		} else {
			pullSuccess++
			totalSourceValues += len(r.SourceDVS.DataValues)
		}
	}

	orchestrations = append(orchestrations, Orchestration{
		Name: "pull-and-convert",
		Request: OHRequest{
			Path:      fmt.Sprintf("internal://pull-all?combinations=%d", totalCombinations),
			Method:    "BATCH",
			Timestamp: startPull,
		},
		Response: OHResponse{
			Status:  200,
			Headers: map[string]string{"Content-Type": "application/json"},
			Body: fmt.Sprintf(`{"success":%d,"failed":%d,"empty":%d,"totalDataValues":%d}`,
				pullSuccess, pullFail, pullEmpty, totalSourceValues),
			Timestamp: endPull,
		},
	})

	log.Printf("Pull complete: %d success, %d failed, %d empty in %v",
		pullSuccess, pullFail, pullEmpty, endPull.Sub(startPull))

	// --- Step 2: Push all to target ---
	startPush := time.Now()
	pushResults := PushAll(target, pullResults, cfg.MaxWorkers)
	endPush := time.Now()

	pushSuccess := 0
	pushFail := 0
	totalImported := 0
	totalUpdated := 0
	totalIgnored := 0
	var failedDetails []string

	for _, r := range pushResults {
		if r.Error != nil {
			pushFail++
			failedDetails = append(failedDetails,
				fmt.Sprintf("%s/%s: %v", r.Job.OrgUnit.ID, r.Job.Period, r.Error))
		} else {
			pushSuccess++
			if r.ImportCount != nil {
				totalImported += r.ImportCount.Imported
				totalUpdated += r.ImportCount.Updated
				totalIgnored += r.ImportCount.Ignored
			}
		}
	}

	orchestrations = append(orchestrations, Orchestration{
		Name: "push-to-target",
		Request: OHRequest{
			Path:      fmt.Sprintf("%s/api/dataValueSets", cfg.DHIS2TargetURL),
			Method:    "BATCH-POST",
			Timestamp: startPush,
		},
		Response: OHResponse{
			Status:  200,
			Headers: map[string]string{"Content-Type": "application/json"},
			Body: fmt.Sprintf(`{"success":%d,"failed":%d,"imported":%d,"updated":%d,"ignored":%d}`,
				pushSuccess, pushFail, totalImported, totalUpdated, totalIgnored),
			Timestamp: endPush,
		},
	})

	log.Printf("Push complete: %d success, %d failed (imported=%d, updated=%d, ignored=%d) in %v",
		pushSuccess, pushFail, totalImported, totalUpdated, totalIgnored, endPush.Sub(startPush))

	// --- Build response ---
	status := "Successful"
	httpStatus := 200
	if pushSuccess == 0 && pullSuccess > 0 {
		status = "Failed"
		httpStatus = 502
	} else if pushFail > 0 || pullFail > 0 {
		status = "Completed"
	}

	summary := map[string]interface{}{
		"totalOrgUnits":    len(orgUnits),
		"totalPeriods":     len(periods),
		"periods":          periods,
		"totalCombinations": totalCombinations,
		"pull": map[string]int{
			"success": pullSuccess,
			"failed":  pullFail,
			"empty":   pullEmpty,
		},
		"push": map[string]int{
			"success":  pushSuccess,
			"failed":   pushFail,
			"imported": totalImported,
			"updated":  totalUpdated,
			"ignored":  totalIgnored,
		},
		"duration": time.Since(startTotal).String(),
	}
	if len(failedDetails) > 0 {
		summary["errors"] = failedDetails
	}

	respBody, _ := json.MarshalIndent(summary, "", "  ")

	openhimResp := OpenHIMResponse{
		XMediatorURN: cfg.MediatorURN,
		Status:       status,
		Response: OHResponse{
			Status:    httpStatus,
			Headers:   map[string]string{"Content-Type": "application/json"},
			Body:      string(respBody),
			Timestamp: time.Now(),
		},
		Orchestrations: orchestrations,
		Properties: map[string]string{
			"pull.success":  strconv.Itoa(pullSuccess),
			"pull.failed":   strconv.Itoa(pullFail),
			"push.success":  strconv.Itoa(pushSuccess),
			"push.failed":   strconv.Itoa(pushFail),
			"push.imported": strconv.Itoa(totalImported),
			"push.updated":  strconv.Itoa(totalUpdated),
		},
	}

	w.Header().Set("Content-Type", "application/json+openhim")
	json.NewEncoder(w).Encode(openhimResp)

	log.Printf("Sync completed in %v — pull: %d/%d, push: %d/%d",
		time.Since(startTotal), pullSuccess, totalCombinations, pushSuccess, pullSuccess)
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
