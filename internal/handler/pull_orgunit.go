package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/didate/dhis2-sync-mediator/internal/config"
	"github.com/didate/dhis2-sync-mediator/internal/dhis2"
	"github.com/didate/dhis2-sync-mediator/internal/fhir"
	"github.com/didate/dhis2-sync-mediator/internal/openhim"
)

func HandlePullOrgUnit(w http.ResponseWriter, r *http.Request, cfg *config.Config, ohc *openhim.OpenHIMClient) {
	log.Printf("Received %s %s", r.Method, r.URL.String())

	transactionID := r.Header.Get("X-OpenHIM-TransactionID")

	ouLevel := cfg.DefaultOULevel
	if v := r.URL.Query().Get("ouLevel"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			ouLevel = n
		}
	}

	openhim.RespondAccepted(w, cfg.MediatorURN, "Pull org units started")

	go func() {
		startTotal := time.Now()
		src := dhis2.NewDHIS2Client(cfg.DHIS2SourceURL, cfg.DHIS2SourcePAT)
		hapi := fhir.NewHAPIClient(cfg.HAPIFhirURL)
		var orchestrations []openhim.Orchestration

		// Fetch org units from DHIS2
		startFetch := time.Now()
		orgUnits, err := src.FetchOrgUnits(ouLevel)
		endFetch := time.Now()

		if err != nil {
			log.Printf("Fetch org units error: %v", err)
			ohc.UpdateTransactionFailed(transactionID, cfg.MediatorURN,
				fmt.Sprintf("Failed to fetch org units: %v", err))
			return
		}

		orchestrations = append(orchestrations, openhim.Orchestration{
			Name: "fetch-org-units-from-dhis2",
			Request: openhim.OHRequest{
				Path:      fmt.Sprintf("%s/api/organisationUnits?level=%d", cfg.DHIS2SourceURL, ouLevel),
				Method:    "GET",
				Headers:   map[string]string{"Authorization": "ApiToken ***"},
				Timestamp: startFetch,
			},
			Response: openhim.OHResponse{
				Status:    200,
				Headers:   map[string]string{"Content-Type": "application/json"},
				Body:      fmt.Sprintf(`{"count":%d}`, len(orgUnits)),
				Timestamp: endFetch,
			},
		})

		log.Printf("Fetched %d org units at level %d", len(orgUnits), ouLevel)

		// Save as FHIR Locations to HAPI
		startSave := time.Now()
		success := 0
		failed := 0

		jobs := make(chan dhis2.OrgUnit, len(orgUnits))
		var mu sync.Mutex
		var wg sync.WaitGroup

		for i := 0; i < cfg.MaxWorkers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for ou := range jobs {
					loc := fhir.OrgUnitToLocation(ou, cfg.OUIdentifierSystem)
					if err := hapi.PutLocation(loc); err != nil {
						log.Printf("Save Location failed [%s]: %v", ou.ID, err)
						mu.Lock()
						failed++
						mu.Unlock()
					} else {
						mu.Lock()
						success++
						mu.Unlock()
					}
				}
			}()
		}

		for _, ou := range orgUnits {
			jobs <- ou
		}
		close(jobs)
		wg.Wait()
		endSave := time.Now()

		orchestrations = append(orchestrations, openhim.Orchestration{
			Name: "save-locations-to-hapi-fhir",
			Request: openhim.OHRequest{
				Path:      fmt.Sprintf("%s/Location", cfg.HAPIFhirURL),
				Method:    "PUT",
				Timestamp: startSave,
			},
			Response: openhim.OHResponse{
				Status:    200,
				Headers:   map[string]string{"Content-Type": "application/json"},
				Body:      fmt.Sprintf(`{"success":%d,"failed":%d,"total":%d}`, success, failed, len(orgUnits)),
				Timestamp: endSave,
			},
		})

		log.Printf("Saved Locations to HAPI: %d success, %d failed in %v", success, failed, endSave.Sub(startSave))

		// Update OpenHIM transaction
		status := "Successful"
		if failed > 0 && success > 0 {
			status = "Completed"
		} else if success == 0 {
			status = "Failed"
		}

		summary, _ := json.MarshalIndent(map[string]interface{}{
			"orgUnitsFound": len(orgUnits),
			"saved":         success,
			"failed":        failed,
			"ouLevel":       ouLevel,
			"duration":      time.Since(startTotal).String(),
		}, "", "  ")

		ohc.UpdateTransaction(transactionID, map[string]interface{}{
			"status": status,
			"response": map[string]interface{}{
				"status":    200,
				"headers":   map[string]string{"Content-Type": "application/json"},
				"body":      string(summary),
				"timestamp": time.Now(),
			},
			"orchestrations": orchestrations,
			"properties": map[string]string{
				"orgUnits.total": strconv.Itoa(len(orgUnits)),
				"orgUnits.saved": strconv.Itoa(success),
			},
		})

		log.Printf("Pull org units completed in %v", time.Since(startTotal))
	}()
}
