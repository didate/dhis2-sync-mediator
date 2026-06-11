package fhir

import "github.com/didate/dhis2-sync-mediator/internal/dhis2"

// FHIRLocation represents a FHIR R4 Location resource.
type FHIRLocation struct {
	ResourceType string       `json:"resourceType"`
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Status       string       `json:"status"`
	Identifier   []Identifier `json:"identifier,omitempty"`
}

type Identifier struct {
	System string `json:"system"`
	Value  string `json:"value"`
}

func OrgUnitToLocation(ou dhis2.OrgUnit, identifierSystem string) *FHIRLocation {
	return &FHIRLocation{
		ResourceType: "Location",
		ID:           ou.ID,
		Name:         ou.Name,
		Status:       "active",
		Identifier: []Identifier{{
			System: identifierSystem,
			Value:  ou.ID,
		}},
	}
}

func LocationToOrgUnit(loc *FHIRLocation) dhis2.OrgUnit {
	id := loc.ID
	if len(loc.Identifier) > 0 {
		id = loc.Identifier[0].Value
	}
	return dhis2.OrgUnit{
		ID:   id,
		Name: loc.Name,
	}
}
