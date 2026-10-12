package app

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestOrganizationPhishingContract(t *testing.T) {
	data, err := os.ReadFile("../../../contracts/organization-phishing-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Request  OrganizationRequest  `json:"request"`
		Response OrganizationResponse `json:"response"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if !fixture.Request.Valid() || fixture.Request.Source != "phishing" || fixture.Response.Rows[0].Phishing.Score != 80 {
		t.Fatal("invalid shared contract")
	}
	encoded, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	var before, after any
	if json.Unmarshal(data, &before) != nil || json.Unmarshal(encoded, &after) != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("contract fields lost")
	}
}
