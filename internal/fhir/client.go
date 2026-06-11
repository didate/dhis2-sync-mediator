package fhir

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type HAPIClient struct {
	BaseURL string
	http    *http.Client
}

type FHIRBundle struct {
	ResourceType string        `json:"resourceType"`
	Type         string        `json:"type"`
	Total        int           `json:"total"`
	Link         []BundleLink  `json:"link"`
	Entry        []BundleEntry `json:"entry"`
}

type BundleLink struct {
	Relation string `json:"relation"`
	URL      string `json:"url"`
}

type BundleEntry struct {
	Resource json.RawMessage `json:"resource"`
}

func NewHAPIClient(baseURL string) *HAPIClient {
	return &HAPIClient{
		BaseURL: baseURL,
		http: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// PutLocation creates or updates a Location resource (idempotent via PUT).
func (c *HAPIClient) PutLocation(loc *FHIRLocation) error {
	body, _ := json.Marshal(loc)
	url := fmt.Sprintf("%s/Location/%s", c.BaseURL, loc.ID)

	req, err := http.NewRequest("PUT", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/fhir+json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("put location failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 300 {
		return fmt.Errorf("put location returned %d", resp.StatusCode)
	}
	return nil
}

// GetAllLocations retrieves all Location resources with the given identifier system.
func (c *HAPIClient) GetAllLocations(system string) ([]FHIRLocation, error) {
	url := fmt.Sprintf("%s/Location?identifier=%s|&_count=100", c.BaseURL, system)
	var locations []FHIRLocation

	for url != "" {
		bundle, err := c.fetchBundle(url)
		if err != nil {
			return nil, err
		}

		for _, entry := range bundle.Entry {
			var loc FHIRLocation
			if err := json.Unmarshal(entry.Resource, &loc); err == nil {
				locations = append(locations, loc)
			}
		}

		url = nextLink(bundle)
	}

	return locations, nil
}

// PutMeasureReport creates or updates a MeasureReport resource (idempotent via PUT).
func (c *HAPIClient) PutMeasureReport(mr *MeasureReport) error {
	body, _ := json.Marshal(mr)
	url := fmt.Sprintf("%s/MeasureReport/%s", c.BaseURL, mr.ID)

	req, err := http.NewRequest("PUT", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/fhir+json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("put measure report failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("put measure report returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// GetAllMeasureReports retrieves all MeasureReports matching a given measure URL.
func (c *HAPIClient) GetAllMeasureReports(measureURL string) ([]MeasureReport, error) {
	url := fmt.Sprintf("%s/MeasureReport?measure=%s&_count=100", c.BaseURL, measureURL)
	var reports []MeasureReport

	for url != "" {
		bundle, err := c.fetchBundle(url)
		if err != nil {
			return nil, err
		}

		for _, entry := range bundle.Entry {
			var mr MeasureReport
			if err := json.Unmarshal(entry.Resource, &mr); err == nil {
				reports = append(reports, mr)
			}
		}

		url = nextLink(bundle)
	}

	return reports, nil
}

func (c *HAPIClient) fetchBundle(url string) (*FHIRBundle, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/fhir+json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch bundle failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fhir server returned %d: %s", resp.StatusCode, string(body))
	}

	var bundle FHIRBundle
	if err := json.Unmarshal(body, &bundle); err != nil {
		return nil, fmt.Errorf("parse bundle: %w", err)
	}

	return &bundle, nil
}

func nextLink(b *FHIRBundle) string {
	for _, l := range b.Link {
		if l.Relation == "next" {
			return l.URL
		}
	}
	return ""
}
