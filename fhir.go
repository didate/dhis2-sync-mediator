package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// FHIR MeasureReport structures (R4)

type MeasureReport struct {
	ResourceType string              `json:"resourceType"`
	ID           string              `json:"id"`
	Status       string              `json:"status"`
	Type         string              `json:"type"`
	Measure      string              `json:"measure,omitempty"`
	Subject      *Reference          `json:"subject,omitempty"`
	Date         string              `json:"date"`
	Period       FHIRPeriod          `json:"period"`
	Group        []MeasureReportGroup `json:"group,omitempty"`
}

type FHIRPeriod struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type Reference struct {
	Reference string `json:"reference"`
}

type CodeableConcept struct {
	Coding []Coding `json:"coding,omitempty"`
	Text   string   `json:"text,omitempty"`
}

type Coding struct {
	System string `json:"system,omitempty"`
	Code   string `json:"code"`
}

type MeasureReportGroup struct {
	Code         *CodeableConcept                `json:"code,omitempty"`
	Population   []MeasureReportPopulation       `json:"population,omitempty"`
	MeasureScore *Quantity                       `json:"measureScore,omitempty"`
}

type MeasureReportPopulation struct {
	Code  *CodeableConcept `json:"code,omitempty"`
	Count *int             `json:"count,omitempty"`
}

type Quantity struct {
	Value float64 `json:"value"`
}

// DataValueSetToMeasureReport converts a DHIS2 DataValueSet into a FHIR MeasureReport.
// Each DataValue becomes a group entry with:
//   - group.code = dataElement ID
//   - group.population.code = categoryOptionCombo
//   - group.measureScore or population.count = value
func DataValueSetToMeasureReport(dvs *DataValueSet, dhis2BaseURL string) (*MeasureReport, error) {
	period, err := parseDHIS2Period(dvs.Period)
	if err != nil {
		return nil, fmt.Errorf("parse period %q: %w", dvs.Period, err)
	}

	mr := &MeasureReport{
		ResourceType: "MeasureReport",
		ID:           uuid.New().String(),
		Status:       "complete",
		Type:         "summary",
		Measure:      dhis2BaseURL + "/api/dataSets/" + dvs.DataSet,
		Subject:      &Reference{Reference: "Location/" + dvs.OrgUnit},
		Date:         time.Now().UTC().Format(time.RFC3339),
		Period:       *period,
	}

	for _, dv := range dvs.DataValues {
		group := MeasureReportGroup{
			Code: &CodeableConcept{
				Coding: []Coding{{
					System: dhis2BaseURL + "/api/dataElements",
					Code:   dv.DataElement,
				}},
			},
		}

		// Try to parse value as integer for population count,
		// otherwise use measureScore for decimal values.
		if intVal, err := strconv.Atoi(dv.Value); err == nil {
			pop := MeasureReportPopulation{
				Count: &intVal,
			}
			if dv.CategoryOptionCombo != "" {
				pop.Code = &CodeableConcept{
					Coding: []Coding{{
						System: dhis2BaseURL + "/api/categoryOptionCombos",
						Code:   dv.CategoryOptionCombo,
					}},
				}
			}
			group.Population = []MeasureReportPopulation{pop}
		} else if floatVal, err := strconv.ParseFloat(dv.Value, 64); err == nil {
			group.MeasureScore = &Quantity{Value: floatVal}
		} else {
			// Non-numeric value: store as text in the code
			group.Code.Text = dv.Value
		}

		mr.Group = append(mr.Group, group)
	}

	return mr, nil
}

// parseDHIS2Period converts DHIS2 period formats to FHIR Period (start/end dates).
// Supported formats: daily (20250101), weekly (2025W40), monthly (202501),
// quarterly (2025Q1), yearly (2025).
func parseDHIS2Period(p string) (*FHIRPeriod, error) {
	p = strings.TrimSpace(p)

	// Weekly: 2025W40
	if strings.Contains(p, "W") {
		parts := strings.Split(p, "W")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid weekly period: %s", p)
		}
		year, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("invalid year in weekly period: %s", p)
		}
		week, err := strconv.Atoi(parts[1])
		if err != nil {
			return nil, fmt.Errorf("invalid week in weekly period: %s", p)
		}
		start := weekStart(year, week)
		end := start.AddDate(0, 0, 6)
		return &FHIRPeriod{
			Start: start.Format("2006-01-02"),
			End:   end.Format("2006-01-02"),
		}, nil
	}

	// Quarterly: 2025Q1
	if strings.Contains(p, "Q") {
		parts := strings.Split(p, "Q")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid quarterly period: %s", p)
		}
		year, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("invalid year in quarterly period: %s", p)
		}
		q, err := strconv.Atoi(parts[1])
		if err != nil || q < 1 || q > 4 {
			return nil, fmt.Errorf("invalid quarter in period: %s", p)
		}
		startMonth := time.Month((q-1)*3 + 1)
		start := time.Date(year, startMonth, 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 3, -1)
		return &FHIRPeriod{
			Start: start.Format("2006-01-02"),
			End:   end.Format("2006-01-02"),
		}, nil
	}

	// Daily: 20250101 (8 digits)
	if len(p) == 8 {
		t, err := time.Parse("20060102", p)
		if err != nil {
			return nil, fmt.Errorf("invalid daily period: %s", p)
		}
		d := t.Format("2006-01-02")
		return &FHIRPeriod{Start: d, End: d}, nil
	}

	// Monthly: 202501 (6 digits)
	if len(p) == 6 {
		t, err := time.Parse("200601", p)
		if err != nil {
			return nil, fmt.Errorf("invalid monthly period: %s", p)
		}
		start := t
		end := t.AddDate(0, 1, -1)
		return &FHIRPeriod{
			Start: start.Format("2006-01-02"),
			End:   end.Format("2006-01-02"),
		}, nil
	}

	// Yearly: 2025 (4 digits)
	if len(p) == 4 {
		year, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("invalid yearly period: %s", p)
		}
		return &FHIRPeriod{
			Start: fmt.Sprintf("%d-01-01", year),
			End:   fmt.Sprintf("%d-12-31", year),
		}, nil
	}

	return nil, fmt.Errorf("unsupported period format: %s", p)
}

// MeasureReportToDataValueSet converts a FHIR MeasureReport back to a DHIS2 DataValueSet.
// It extracts the dataSet, orgUnit, and period from the MeasureReport metadata,
// and maps each group back to a DataValue.
func MeasureReportToDataValueSet(mr *MeasureReport) (*DataValueSet, error) {
	// Extract dataSet ID from Measure URL (last path segment)
	dataSetID := lastPathSegment(mr.Measure)

	// Extract orgUnit from Subject reference ("Location/UID")
	orgUnit := ""
	if mr.Subject != nil {
		orgUnit = lastPathSegment(mr.Subject.Reference)
	}

	// Convert FHIR period back to DHIS2 period is not needed here
	// since we preserve the original period in the DataValueSet from source.
	// We'll pass it through from the original DVS.

	dvs := &DataValueSet{
		DataSet: dataSetID,
		OrgUnit: orgUnit,
	}

	for _, g := range mr.Group {
		dv := DataValue{}

		// dataElement from group.code
		if g.Code != nil && len(g.Code.Coding) > 0 {
			dv.DataElement = g.Code.Coding[0].Code
		}

		// categoryOptionCombo + value from population
		if len(g.Population) > 0 {
			pop := g.Population[0]
			if pop.Code != nil && len(pop.Code.Coding) > 0 {
				dv.CategoryOptionCombo = pop.Code.Coding[0].Code
			}
			if pop.Count != nil {
				dv.Value = strconv.Itoa(*pop.Count)
			}
		} else if g.MeasureScore != nil {
			dv.Value = strconv.FormatFloat(g.MeasureScore.Value, 'f', -1, 64)
		}

		if dv.DataElement != "" && dv.Value != "" {
			dvs.DataValues = append(dvs.DataValues, dv)
		}
	}

	return dvs, nil
}

func lastPathSegment(s string) string {
	parts := strings.Split(s, "/")
	return parts[len(parts)-1]
}

// weekStart returns the Monday of ISO week for the given year and week number.
func weekStart(year, week int) time.Time {
	// Jan 4 is always in ISO week 1
	jan4 := time.Date(year, 1, 4, 0, 0, 0, 0, time.UTC)
	// Find Monday of week 1
	offset := int(time.Monday - jan4.Weekday())
	if jan4.Weekday() == time.Sunday {
		offset = -6
	}
	week1Monday := jan4.AddDate(0, 0, offset)
	return week1Monday.AddDate(0, 0, (week-1)*7)
}
