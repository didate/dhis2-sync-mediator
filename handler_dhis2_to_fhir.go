package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"
)

func handleDHIS2ToFHIR(w http.ResponseWriter, r *http.Request, cfg *Config, ohc *OpenHIMClient) {
	log.Printf("Received %s %s", r.Method, r.URL.String())

	transactionID := r.Header.Get("X-OpenHIM-TransactionID")

	dataSet := r.URL.Query().Get("dataSet")
	if dataSet == "" {
		respondError(w, cfg.MediatorURN, http.StatusBadRequest, "Missing required query param: dataSet")
		return
	}

	weeks := cfg.DefaultWeeks
	if v := r.URL.Query().Get("weeks"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			weeks = n
		}
	}

	var periods []string
	if p := r.URL.Query().Get("period"); p != "" {
		periods = []string{p}
	} else {
		periods = GenerateWeekPeriods(weeks)
	}

	respondAccepted(w, cfg.MediatorURN, "DHIS2 to FHIR conversion started")

	go func() {
		startTotal := time.Now()
		src := NewDHIS2Client(cfg.DHIS2SourceURL, cfg.DHIS2SourcePAT)
		hapi := NewHAPIClient(cfg.HAPIFhirURL)
		var orchestrations []Orchestration

		// Get org units from HAPI FHIR (Locations)
		startLoc := time.Now()
		locations, err := hapi.GetAllLocations(cfg.DHIS2SourceURL + "/api/organisationUnits")
		endLoc := time.Now()

		if err != nil {
			log.Printf("Fetch locations from HAPI error: %v", err)
			ohc.updateTransactionFailed(transactionID, cfg.MediatorURN,
				fmt.Sprintf("Failed to fetch locations from HAPI: %v", err))
			return
		}

		orgUnits := make([]OrgUnit, 0, len(locations))
		for _, loc := range locations {
			orgUnits = append(orgUnits, LocationToOrgUnit(&loc))
		}

		orchestrations = append(orchestrations, Orchestration{
			Name: "fetch-locations-from-hapi-fhir",
			Request: OHRequest{
				Path:      fmt.Sprintf("%s/Location", cfg.HAPIFhirURL),
				Method:    "GET",
				Timestamp: startLoc,
			},
			Response: OHResponse{
				Status:    200,
				Headers:   map[string]string{"Content-Type": "application/json"},
				Body:      fmt.Sprintf(`{"count":%d}`, len(orgUnits)),
				Timestamp: endLoc,
			},
		})

		log.Printf("Got %d org units from HAPI FHIR Locations", len(orgUnits))

		// Pull from DHIS2 and save as MeasureReport to HAPI
		type job struct {
			OrgUnit OrgUnit
			Period  string
		}

		totalCombinations := len(orgUnits) * len(periods)
		jobs := make(chan job, totalCombinations)
		var mu sync.Mutex
		var wg sync.WaitGroup

		pullSuccess := 0
		pullFail := 0
		pullEmpty := 0
		saveSuccess := 0
		saveFail := 0

		startPull := time.Now()

		for i := 0; i < cfg.MaxWorkers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := range jobs {
					dvs, _, _, err := src.FetchDataValueSet(dataSet, j.OrgUnit.ID, j.Period)
					if err != nil {
						log.Printf("Pull failed [OU=%s, Period=%s]: %v", j.OrgUnit.ID, j.Period, err)
						mu.Lock()
						pullFail++
						mu.Unlock()
						continue
					}

					if len(dvs.DataValues) == 0 {
						mu.Lock()
						pullEmpty++
						mu.Unlock()
						continue
					}

					mr, err := DataValueSetToMeasureReport(dvs, cfg.DHIS2SourceURL)
					if err != nil {
						log.Printf("FHIR conversion failed [OU=%s, Period=%s]: %v", j.OrgUnit.ID, j.Period, err)
						mu.Lock()
						pullFail++
						mu.Unlock()
						continue
					}

					if err := hapi.PutMeasureReport(mr); err != nil {
						log.Printf("Save MeasureReport failed [OU=%s, Period=%s]: %v", j.OrgUnit.ID, j.Period, err)
						mu.Lock()
						saveFail++
						mu.Unlock()
						continue
					}

					mu.Lock()
					pullSuccess++
					saveSuccess++
					mu.Unlock()
				}
			}()
		}

		for _, ou := range orgUnits {
			for _, period := range periods {
				jobs <- job{OrgUnit: ou, Period: period}
			}
		}
		close(jobs)
		wg.Wait()
		endPull := time.Now()

		orchestrations = append(orchestrations, Orchestration{
			Name: "pull-dhis2-and-save-to-hapi",
			Request: OHRequest{
				Path:      fmt.Sprintf("internal://dhis2-to-fhir?combinations=%d", totalCombinations),
				Method:    "BATCH",
				Timestamp: startPull,
			},
			Response: OHResponse{
				Status:  200,
				Headers: map[string]string{"Content-Type": "application/json"},
				Body: fmt.Sprintf(`{"pullSuccess":%d,"pullFail":%d,"pullEmpty":%d,"saved":%d,"saveFail":%d}`,
					pullSuccess, pullFail, pullEmpty, saveSuccess, saveFail),
				Timestamp: endPull,
			},
		})

		log.Printf("DHIS2 to FHIR complete: pull=%d/%d, saved=%d, empty=%d in %v",
			pullSuccess, totalCombinations, saveSuccess, pullEmpty, endPull.Sub(startPull))

		// Update OpenHIM transaction
		status := "Successful"
		if pullFail > 0 || saveFail > 0 {
			status = "Completed"
		}
		if pullSuccess == 0 && saveSuccess == 0 {
			status = "Failed"
		}

		summary, _ := json.MarshalIndent(map[string]interface{}{
			"totalOrgUnits":     len(orgUnits),
			"totalPeriods":      len(periods),
			"periods":           periods,
			"totalCombinations": totalCombinations,
			"pullSuccess":       pullSuccess,
			"pullFail":          pullFail,
			"pullEmpty":         pullEmpty,
			"savedToFHIR":       saveSuccess,
			"saveFail":          saveFail,
			"duration":          time.Since(startTotal).String(),
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
				"combinations": strconv.Itoa(totalCombinations),
				"saved":        strconv.Itoa(saveSuccess),
				"failed":       strconv.Itoa(pullFail + saveFail),
			},
		})

		log.Printf("DHIS2 to FHIR completed in %v", time.Since(startTotal))
	}()
}
