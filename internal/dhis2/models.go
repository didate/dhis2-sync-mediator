package dhis2

import "encoding/json"

type OrgUnit struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type OrgUnitsResponse struct {
	OrganisationUnits []OrgUnit `json:"organisationUnits"`
}

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

type ImportCount struct {
	Imported int `json:"imported"`
	Updated  int `json:"updated"`
	Ignored  int `json:"ignored"`
	Deleted  int `json:"deleted"`
}

func ParseImportCount(body []byte) *ImportCount {
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
