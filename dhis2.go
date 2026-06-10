package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// DataValueSet : structure JSON retournée par /api/dataValueSets
type DataValueSet struct {
	DataSet              string      `json:"dataSet,omitempty"`
	CompleteDate         string      `json:"completeDate,omitempty"`
	Period               string      `json:"period,omitempty"`
	OrgUnit              string      `json:"orgUnit,omitempty"`
	AttributeOptionCombo string      `json:"attributeOptionCombo,omitempty"`
	DataValues           []DataValue `json:"dataValues"`
}

type DataValue struct {
	DataElement          string `json:"dataElement"`
	Period               string `json:"period,omitempty"`
	OrgUnit              string `json:"orgUnit,omitempty"`
	CategoryOptionCombo  string `json:"categoryOptionCombo,omitempty"`
	AttributeOptionCombo string `json:"attributeOptionCombo,omitempty"`
	Value                string `json:"value"`
	StoredBy             string `json:"storedBy,omitempty"`
	Created              string `json:"created,omitempty"`
	LastUpdated          string `json:"lastUpdated,omitempty"`
	Comment              string `json:"comment,omitempty"`
	Followup             bool   `json:"followup,omitempty"`
}

type DHIS2Client struct {
	BaseURL string
	PAT     string
	http    *http.Client
}

func NewDHIS2Client(baseURL, pat string) *DHIS2Client {
	return &DHIS2Client{
		BaseURL: baseURL,
		PAT:     pat,
		http: &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: false},
			},
		},
	}
}

func (c *DHIS2Client) FetchDataValueSet(dataSet, orgUnit, period string) (*DataValueSet, []byte, string, error) {
	params := url.Values{}
	params.Set("dataSet", dataSet)
	params.Set("orgUnit", orgUnit)
	params.Set("period", period)

	endpoint := fmt.Sprintf("%s/api/dataValueSets?%s", c.BaseURL, params.Encode())

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, nil, endpoint, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "ApiToken "+c.PAT)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, endpoint, fmt.Errorf("dhis2 call failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, endpoint, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode >= 300 {
		return nil, body, endpoint, fmt.Errorf("dhis2 returned %d: %s", resp.StatusCode, string(body))
	}

	var dvs DataValueSet
	if err := json.Unmarshal(body, &dvs); err != nil {
		return nil, body, endpoint, fmt.Errorf("parse dataValueSet: %w", err)
	}

	return &dvs, body, endpoint, nil
}

func (c *DHIS2Client) PostDataValueSet(dvs *DataValueSet) ([]byte, string, error) {
	endpoint := fmt.Sprintf("%s/api/dataValueSets", c.BaseURL)

	body, err := json.Marshal(dvs)
	if err != nil {
		return nil, endpoint, fmt.Errorf("marshal dataValueSet: %w", err)
	}

	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, endpoint, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "ApiToken "+c.PAT)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, endpoint, fmt.Errorf("dhis2 call failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, endpoint, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode >= 300 {
		return respBody, endpoint, fmt.Errorf("dhis2 returned %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, endpoint, nil
}
