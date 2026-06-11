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
	"github.com/didate/dhis2-sync-mediator/internal/period"
)

func HandleFHIRToDHIS2(w http.ResponseWriter, r *http.Request, cfg *config.Config, ohc *openhim.OpenHIMClient) {
	log.Printf("Received %s %s", r.Method, r.URL.String())

	transactionID := r.Header.Get("X-OpenHIM-TransactionID")

	dataSet := r.URL.Query().Get("dataSet")
	if dataSet == "" {
		openhim.RespondError(w, cfg.MediatorURN, http.StatusBadRequest, "Missing required query param: dataSet")
		return
	}

	// Determine which periods to push
	var allowedPeriods map[string]bool
	if p := r.URL.Query().Get("period"); p != "" {
		allowedPeriods = map[string]bool{p: true}
	} else {
		weeks := cfg.DefaultWeeks
		if v := r.URL.Query().Get("weeks"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				weeks = n
			}
		}
		periods := period.GenerateWeekPeriods(weeks)
		allowedPeriods = make(map[string]bool, len(periods))
		for _, p := range periods {
			allowedPeriods[p] = true
		}
	}

	openhim.RespondAccepted(w, cfg.MediatorURN, "FHIR to DHIS2 push started")

	go func() {
		startTotal := time.Now()
		target := dhis2.NewDHIS2Client(cfg.DHIS2TargetURL, cfg.DHIS2TargetPAT)
		hapi := fhir.NewHAPIClient(cfg.HAPIFhirURL)
		var orchestrations []openhim.Orchestration

		// Fetch MeasureReports from HAPI FHIR
		measureURL := cfg.DHIS2SourceURL + "/api/dataSets/" + dataSet
		startFetch := time.Now()
		reports, err := hapi.GetAllMeasureReports(measureURL)
		endFetch := time.Now()

		if err != nil {
			log.Printf("Fetch MeasureReports from HAPI error: %v", err)
			ohc.UpdateTransactionFailed(transactionID, cfg.MediatorURN,
				fmt.Sprintf("Failed to fetch MeasureReports: %v", err))
			return
		}

		orchestrations = append(orchestrations, openhim.Orchestration{
			Name: "fetch-measure-reports-from-hapi",
			Request: openhim.OHRequest{
				Path:      fmt.Sprintf("%s/MeasureReport?measure=%s", cfg.HAPIFhirURL, measureURL),
				Method:    "GET",
				Timestamp: startFetch,
			},
			Response: openhim.OHResponse{
				Status:    200,
				Headers:   map[string]string{"Content-Type": "application/json"},
				Body:      fmt.Sprintf(`{"count":%d}`, len(reports)),
				Timestamp: endFetch,
			},
		})

		// Filter by allowed periods
		var filtered []fhir.MeasureReport
		for _, mr := range reports {
			for _, ext := range mr.Extension {
				if ext.URL == fhir.ExtPeriod && allowedPeriods[ext.ValueString] {
					filtered = append(filtered, mr)
					break
				}
			}
		}

		log.Printf("Got %d MeasureReports from HAPI FHIR, %d match period filter", len(reports), len(filtered))
		reports = filtered

		if len(reports) == 0 {
			ohc.UpdateTransaction(transactionID, map[string]interface{}{
				"status": "Completed",
				"response": map[string]interface{}{
					"status":    200,
					"headers":   map[string]string{"Content-Type": "application/json"},
					"body":      `{"message":"No MeasureReports found to push"}`,
					"timestamp": time.Now(),
				},
				"orchestrations": orchestrations,
			})
			return
		}

		// Convert and push to target DHIS2
		startPush := time.Now()
		var mu sync.Mutex
		var wg sync.WaitGroup

		pushSuccess := 0
		pushFail := 0
		totalImported := 0
		totalUpdated := 0
		totalIgnored := 0
		var failedDetails []string

		jobs := make(chan fhir.MeasureReport, len(reports))

		for i := 0; i < cfg.MaxWorkers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for mr := range jobs {
					dvs, err := fhir.MeasureReportToDataValueSet(&mr)
					if err != nil {
						log.Printf("Convert MeasureReport %s failed: %v", mr.ID, err)
						mu.Lock()
						pushFail++
						failedDetails = append(failedDetails, fmt.Sprintf("%s: conversion error: %v", mr.ID, err))
						mu.Unlock()
						continue
					}

					respBody, _, err := target.PostDataValueSet(dvs)
					if err != nil {
						log.Printf("Push failed [%s]: %v", mr.ID, err)
						mu.Lock()
						pushFail++
						failedDetails = append(failedDetails, fmt.Sprintf("%s: %v", mr.ID, err))
						mu.Unlock()
						continue
					}

					ic := dhis2.ParseImportCount(respBody)
					mu.Lock()
					pushSuccess++
					totalImported += ic.Imported
					totalUpdated += ic.Updated
					totalIgnored += ic.Ignored
					mu.Unlock()

					log.Printf("Push OK [%s]: imported=%d updated=%d", mr.ID, ic.Imported, ic.Updated)
				}
			}()
		}

		for _, mr := range reports {
			jobs <- mr
		}
		close(jobs)
		wg.Wait()
		endPush := time.Now()

		orchestrations = append(orchestrations, openhim.Orchestration{
			Name: "push-to-target-dhis2",
			Request: openhim.OHRequest{
				Path:      fmt.Sprintf("%s/api/dataValueSets", cfg.DHIS2TargetURL),
				Method:    "BATCH-POST",
				Timestamp: startPush,
			},
			Response: openhim.OHResponse{
				Status:  200,
				Headers: map[string]string{"Content-Type": "application/json"},
				Body: fmt.Sprintf(`{"success":%d,"failed":%d,"imported":%d,"updated":%d,"ignored":%d}`,
					pushSuccess, pushFail, totalImported, totalUpdated, totalIgnored),
				Timestamp: endPush,
			},
		})

		log.Printf("FHIR to DHIS2 complete: push=%d/%d (imported=%d, updated=%d) in %v",
			pushSuccess, len(reports), totalImported, totalUpdated, endPush.Sub(startPush))

		// Update OpenHIM transaction
		status := "Successful"
		if pushFail > 0 && pushSuccess > 0 {
			status = "Completed"
		} else if pushSuccess == 0 {
			status = "Failed"
		}

		summaryMap := map[string]interface{}{
			"totalMeasureReports": len(reports),
			"pushSuccess":        pushSuccess,
			"pushFail":           pushFail,
			"imported":           totalImported,
			"updated":            totalUpdated,
			"ignored":            totalIgnored,
			"duration":           time.Since(startTotal).String(),
		}
		if len(failedDetails) > 0 {
			summaryMap["errors"] = failedDetails
		}
		summary, _ := json.MarshalIndent(summaryMap, "", "  ")

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
				"push.success":  strconv.Itoa(pushSuccess),
				"push.failed":   strconv.Itoa(pushFail),
				"push.imported": strconv.Itoa(totalImported),
				"push.updated":  strconv.Itoa(totalUpdated),
			},
		})

		log.Printf("FHIR to DHIS2 completed in %v", time.Since(startTotal))
	}()
}
