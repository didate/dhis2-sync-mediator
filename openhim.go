package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
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
	cfg  *Config
	http *http.Client
}

func NewOpenHIMClient(cfg *Config) *OpenHIMClient {
	tr := &http.Transport{}
	if cfg.OpenHIMTrustSelf {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &OpenHIMClient{
		cfg:  cfg,
		http: &http.Client{Transport: tr, Timeout: 30 * time.Second},
	}
}

// Register tells OpenHIM "I exist, here are my channels".
func (c *OpenHIMClient) Register() error {
	reg := MediatorRegistration{
		URN:         c.cfg.MediatorURN,
		Version:     "0.1.0",
		Name:        "DHIS2 Sync Mediator",
		Description: "Syncs aggregate data between two DHIS2 instances via FHIR",
		DefaultChannelConfig: []ChannelConfig{{
			Name:         "DHIS2 Sync Channel",
			URLPattern:   "^/sync.*$",
			Type:         "http",
			AllowedRoles: []string{"dhis2-sync"},
			Routes: []Endpoint{{
				Name:    "DHIS2 Sync Mediator Route",
				Host:    "celsa-intervalic-shayna.ngrok-free.dev",
				Port:    mustAtoi(c.cfg.MediatorPort),
				Path:    "/sync",
				Primary: true,
				Type:    "http",
				Secured: true,
			}},
		}},
		Endpoints: []Endpoint{{
			Name: "DHIS2 Sync Mediator Endpoint",
			Host: "localhost",
			Port: mustAtoi(c.cfg.MediatorPort),
			Path: "/sync",
			Type: "http",
		}},
	}

	body, _ := json.Marshal(reg)
	req, _ := http.NewRequest("POST", c.cfg.OpenHIMAPIURL+"/mediators", bytes.NewReader(body))
	req.SetBasicAuth(c.cfg.OpenHIMUser, c.cfg.OpenHIMPassword)
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
	log.Printf("Mediator registered with OpenHIM (URN=%s)", c.cfg.MediatorURN)
	return nil
}

// Heartbeat tells OpenHIM "I'm still alive". OpenHIM tracks mediator uptime.
func (c *OpenHIMClient) Heartbeat() {
	ticker := time.NewTicker(10 * time.Second)
	go func() {
		for range ticker.C {
			body := []byte(`{"uptime": 60.5}`)
			url := fmt.Sprintf("%s/mediators/%s/heartbeat", c.cfg.OpenHIMAPIURL, c.cfg.MediatorURN)
			req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
			req.SetBasicAuth(c.cfg.OpenHIMUser, c.cfg.OpenHIMPassword)
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

func mustAtoi(s string) int {
	var n int
	fmt.Sscanf(s, "%d", &n)
	return n
}
