package main

import (
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	OpenHIMAPIURL      string
	OpenHIMUser        string
	OpenHIMPassword    string
	OpenHIMTrustSelf   bool
	MediatorPort       string
	MediatorURN        string
	DHIS2SourceURL     string
	DHIS2SourcePAT     string
	DHIS2TargetURL     string
	DHIS2TargetPAT     string
}

func LoadConfig() *Config {
	_ = godotenv.Load()
	return &Config{
		OpenHIMAPIURL:    os.Getenv("OPENHIM_API_URL"),
		OpenHIMUser:      os.Getenv("OPENHIM_API_USER"),
		OpenHIMPassword:  os.Getenv("OPENHIM_API_PASSWORD"),
		OpenHIMTrustSelf: os.Getenv("OPENHIM_TRUST_SELF_SIGNED") == "true",
		MediatorPort:     getEnvDefault("MEDIATOR_PORT", "8001"),
		MediatorURN:      getEnvDefault("MEDIATOR_URN", "urn:mediator:dhis2-sync"),
		DHIS2SourceURL:   os.Getenv("DHIS2_SOURCE_URL"),
		DHIS2SourcePAT:   os.Getenv("DHIS2_SOURCE_PAT"),
		DHIS2TargetURL:   os.Getenv("DHIS2_TARGET_URL"),
		DHIS2TargetPAT:   os.Getenv("DHIS2_TARGET_PAT"),
	}
}

func getEnvDefault(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
