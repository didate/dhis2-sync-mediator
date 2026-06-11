package openhim

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/didate/dhis2-sync-mediator/internal/config"
)

// MediatorRegistration is the payload OpenHIM expects when a mediator registers.
type MediatorRegistration struct {
	URN                  string          `json:"urn"`
	Version              string          `json:"version"`
	Name                 string          `json:"name"`
	Description          string          `json:"description"`
	DefaultChannelConfig []ChannelConfig `json:"defaultChannelConfig"`
	Endpoints            []Endpoint      `json:"endpoints"`
}

type ChannelConfig struct {
	Name         string     `json:"name"`
	URLPattern   string     `json:"urlPattern"`
	Type         string     `json:"type"`
	AllowedRoles []string   `json:"allow"`
	Routes       []Endpoint `json:"routes"`
}

type Endpoint struct {
	Name    string `json:"name"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Path    string `json:"path,omitempty"`
	Primary bool   `json:"primary,omitempty"`
	Type    string `json:"type"`
	Secured bool   `json:"secured,omitempty"`
}

type OpenHIMClient struct {
	Cfg  *config.Config
	http *http.Client
}

func NewOpenHIMClient(cfg *config.Config) *OpenHIMClient {
	tr := &http.Transport{}
	if cfg.OpenHIMTrustSelf {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &OpenHIMClient{
		Cfg:  cfg,
		http: &http.Client{Transport: tr, Timeout: 30 * time.Second},
	}
}

// Register tells OpenHIM "I exist, here are my channels".
func (c *OpenHIMClient) Register() error {
	reg := MediatorRegistration{
		URN:         c.Cfg.MediatorURN,
		Version:     "0.1.0",
		Name:        "DHIS2 Sync Mediator",
		Description: "Syncs aggregate data between two DHIS2 instances via FHIR",
		DefaultChannelConfig: []ChannelConfig{
			c.channelConfig("DHIS2 Pull OrgUnit Channel", "^/pull-orgunit.*$", "/pull-orgunit"),
			c.channelConfig("DHIS2 to FHIR Channel", "^/dhis2-to-fhir.*$", "/dhis2-to-fhir"),
			c.channelConfig("FHIR to DHIS2 Channel", "^/fhir-to-dhis2.*$", "/fhir-to-dhis2"),
		},
		Endpoints: []Endpoint{
			{Name: "Pull OrgUnit Endpoint", Host: "localhost", Port: mustAtoi(c.Cfg.MediatorPort), Path: "/pull-orgunit", Type: "http"},
			{Name: "DHIS2 to FHIR Endpoint", Host: "localhost", Port: mustAtoi(c.Cfg.MediatorPort), Path: "/dhis2-to-fhir", Type: "http"},
			{Name: "FHIR to DHIS2 Endpoint", Host: "localhost", Port: mustAtoi(c.Cfg.MediatorPort), Path: "/fhir-to-dhis2", Type: "http"},
		},
	}

	body, _ := json.Marshal(reg)
	req, _ := http.NewRequest("POST", c.Cfg.OpenHIMAPIURL+"/mediators", bytes.NewReader(body))
	req.SetBasicAuth(c.Cfg.OpenHIMUser, c.Cfg.OpenHIMPassword)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("register failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("register returned %d: %s", resp.StatusCode, string(bodyBytes))

	}
	log.Printf("Mediator registered with OpenHIM (URN=%s)", c.Cfg.MediatorURN)
	return nil
}

// Heartbeat tells OpenHIM "I'm still alive". OpenHIM tracks mediator uptime.
func (c *OpenHIMClient) Heartbeat() {
	ticker := time.NewTicker(10 * time.Second)
	go func() {
		for range ticker.C {
			body := []byte(`{"uptime": 60.5}`)
			url := fmt.Sprintf("%s/mediators/%s/heartbeat", c.Cfg.OpenHIMAPIURL, c.Cfg.MediatorURN)
			req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
			req.SetBasicAuth(c.Cfg.OpenHIMUser, c.Cfg.OpenHIMPassword)
			req.Header.Set("Content-Type", "application/json")
			resp, err := c.http.Do(req)
			if err != nil {
				log.Printf("heartbeat error: %v", err)
				continue
			}
			resp.Body.Close()
		}
	}()
}

func (c *OpenHIMClient) channelConfig(name, pattern, path string) ChannelConfig {
	return ChannelConfig{
		Name:         name,
		URLPattern:   pattern,
		Type:         "http",
		AllowedRoles: []string{"dhis2-sync"},
		Routes: []Endpoint{{
			Name:    name + " Route",
			Host:    c.Cfg.MediatorHost,
			Port:    mustAtoi(c.Cfg.MediatorPort),
			Path:    path,
			Primary: true,
			Type:    "http",
			Secured: c.Cfg.MediatorScheme == "https",
		}},
	}
}

// UpdateTransaction updates an OpenHIM transaction status after async processing.
func (c *OpenHIMClient) UpdateTransaction(transactionID string, update map[string]interface{}) error {
	body, _ := json.Marshal(update)
	url := fmt.Sprintf("%s/transactions/%s", c.Cfg.OpenHIMAPIURL, transactionID)

	req, err := http.NewRequest("PUT", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.SetBasicAuth(c.Cfg.OpenHIMUser, c.Cfg.OpenHIMPassword)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("update transaction failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("update transaction returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func (c *OpenHIMClient) UpdateTransactionFailed(transactionID, mediatorURN, message string) {
	c.UpdateTransaction(transactionID, map[string]interface{}{
		"status": "Failed",
		"response": map[string]interface{}{
			"status":    502,
			"headers":   map[string]string{"Content-Type": "application/json"},
			"body":      fmt.Sprintf(`{"error":%q}`, message),
			"timestamp": time.Now(),
		},
	})
}

// RespondAccepted sends an immediate 202 response so OpenHIM doesn't timeout.
func RespondAccepted(w http.ResponseWriter, mediatorURN, message string) {
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

func RespondError(w http.ResponseWriter, mediatorURN string, status int, message string) {
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

func mustAtoi(s string) int {
	var n int
	fmt.Sscanf(s, "%d", &n)
	return n
}
