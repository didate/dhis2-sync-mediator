package main

import (
	"encoding/json"
	"log"
	"sync"
)

type PushResult struct {
	Job         SyncJob
	RawResponse []byte
	Endpoint    string
	Error       error
	ImportCount *ImportCount
}

type ImportCount struct {
	Imported int `json:"imported"`
	Updated  int `json:"updated"`
	Ignored  int `json:"ignored"`
	Deleted  int `json:"deleted"`
}

// PushAll sends all successfully pulled dataValueSets to the target DHIS2 concurrently.
func PushAll(target *DHIS2Client, pullResults []SyncResult, workers int) []PushResult {
	// Filter to results that have data to push
	var toPush []SyncResult
	for _, r := range pullResults {
		if r.Error == nil && r.TargetDVS != nil && len(r.TargetDVS.DataValues) > 0 {
			toPush = append(toPush, r)
		}
	}

	if len(toPush) == 0 {
		return nil
	}

	jobs := make(chan SyncResult, len(toPush))
	results := make(chan PushResult, len(toPush))

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for sr := range jobs {
				rawResp, endpoint, err := target.PostDataValueSet(sr.TargetDVS)

				pr := PushResult{
					Job:         sr.Job,
					RawResponse: rawResp,
					Endpoint:    endpoint,
					Error:       err,
				}

				if err != nil {
					log.Printf("Push failed [OU=%s, Period=%s]: %v",
						sr.Job.OrgUnit.ID, sr.Job.Period, err)
				} else {
					pr.ImportCount = parseImportCount(rawResp)
					log.Printf("Push OK [OU=%s, Period=%s]: imported=%d updated=%d",
						sr.Job.OrgUnit.ID, sr.Job.Period,
						pr.ImportCount.Imported, pr.ImportCount.Updated)
				}

				results <- pr
			}
		}()
	}

	for _, r := range toPush {
		jobs <- r
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	var allResults []PushResult
	for r := range results {
		allResults = append(allResults, r)
	}
	return allResults
}

func parseImportCount(body []byte) *ImportCount {
	var resp struct {
		Response struct {
			ImportCount ImportCount `json:"importCount"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return &ImportCount{}
	}
	return &resp.Response.ImportCount
}
