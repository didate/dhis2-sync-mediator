package main

import (
	"log"
	"sync"
)

type SyncJob struct {
	OrgUnit OrgUnit
	Period  string
	DataSet string
}

type SyncResult struct {
	Job       SyncJob
	SourceDVS *DataValueSet
	TargetDVS *DataValueSet
	Error     error
}

// PullAll fetches dataValueSets for all OrgUnit × Period combinations concurrently
// and converts each through the FHIR MeasureReport round-trip.
func PullAll(src *DHIS2Client, dataSet string, orgUnits []OrgUnit, periods []string, dhis2SourceURL string, workers int) []SyncResult {
	jobs := make(chan SyncJob, len(orgUnits)*len(periods))
	results := make(chan SyncResult, len(orgUnits)*len(periods))

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				result := processJob(src, job, dhis2SourceURL)
				results <- result
			}
		}()
	}

	for _, ou := range orgUnits {
		for _, period := range periods {
			jobs <- SyncJob{OrgUnit: ou, Period: period, DataSet: dataSet}
		}
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	var allResults []SyncResult
	for r := range results {
		allResults = append(allResults, r)
	}
	return allResults
}

func processJob(src *DHIS2Client, job SyncJob, dhis2SourceURL string) SyncResult {
	dvs, _, _, err := src.FetchDataValueSet(job.DataSet, job.OrgUnit.ID, job.Period)
	if err != nil {
		log.Printf("Pull failed [OU=%s, Period=%s]: %v", job.OrgUnit.ID, job.Period, err)
		return SyncResult{Job: job, Error: err}
	}

	if len(dvs.DataValues) == 0 {
		log.Printf("No data [OU=%s, Period=%s]", job.OrgUnit.ID, job.Period)
		return SyncResult{Job: job, SourceDVS: dvs}
	}

	// FHIR round-trip: DVS → MeasureReport → DVS
	mr, err := DataValueSetToMeasureReport(dvs, dhis2SourceURL)
	if err != nil {
		log.Printf("FHIR conversion failed [OU=%s, Period=%s]: %v", job.OrgUnit.ID, job.Period, err)
		return SyncResult{Job: job, SourceDVS: dvs, Error: err}
	}

	targetDVS, err := MeasureReportToDataValueSet(mr)
	if err != nil {
		log.Printf("FHIR reverse conversion failed [OU=%s, Period=%s]: %v", job.OrgUnit.ID, job.Period, err)
		return SyncResult{Job: job, SourceDVS: dvs, Error: err}
	}

	// Preserve original DHIS2 metadata
	targetDVS.Period = dvs.Period
	targetDVS.CompleteDate = truncateToDate(dvs.CompleteDate)
	targetDVS.AttributeOptionCombo = dvs.AttributeOptionCombo

	return SyncResult{Job: job, SourceDVS: dvs, TargetDVS: targetDVS}
}
