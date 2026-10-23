package app

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestOrganizationPhishingContract(t *testing.T) {
	checkOrganizationContract(t, "organization-phishing-v1.json")
}

func TestOrganizationConflictContract(t *testing.T) {
	checkOrganizationContract(t, "organization-conflict-v1.json")
}

func checkOrganizationContract(t *testing.T, name string) {
	t.Helper()
	data, err := os.ReadFile("../../../contracts/" + name)
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
	if !fixture.Request.Valid() || name == "organization-phishing-v1.json" && (fixture.Request.Source != "phishing" || fixture.Response.Rows[0].Phishing.Score != 80) || name == "organization-conflict-v1.json" && fixture.Response.ConflictReason != "labels_changed" {
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
