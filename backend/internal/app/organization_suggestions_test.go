package app

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestOrganizationSuggestionContractAndValidation(t *testing.T) {
	data, err := os.ReadFile("../../../contracts/organization-suggestions-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]json.RawMessage
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"request", "choicesRequest", "highestRequest"} {
		var request OrganizationRequest
		if err := json.Unmarshal(fixture[name], &request); err != nil || !request.Valid() {
			t.Fatal("invalid shared request", name, err)
		}
	}
	var response OrganizationResponse
	if err := json.Unmarshal(fixture["response"], &response); err != nil || len(response.Rows) != 1 || len(response.Rows[0].Suggestions) != 3 {
		t.Fatal("invalid shared response", err)
	}
	var request OrganizationRequest
	json.Unmarshal(fixture["choicesRequest"], &request)
	invalid := []OrganizationRequest{}
	bad := request
	bad.SuggestionMode = "highest"
	invalid = append(invalid, bad)
	bad = request
	bad.Operation = "apply"
	bad.ActionID = "action"
	bad.Digest = strings.Repeat("a", 64)
	bad.EmailIDs = nil
	invalid = append(invalid, bad)
	bad = request
	bad.Source = "phishing"
	invalid = append(invalid, bad)
	bad = request
	bad.Choices = append(append([]OrganizationChoice{}, request.Choices...), request.Choices[0])
	invalid = append(invalid, bad)
	bad = request
	bad.Choices = []OrganizationChoice{{EmailID: "other-email", CategoryID: "category-work"}}
	invalid = append(invalid, bad)
	bad = request
	bad.Choices = []OrganizationChoice{{EmailID: "fixture-email", CategoryID: strings.Repeat("a", 101)}}
	invalid = append(invalid, bad)
	bad = request
	bad.SuggestionMode = "all"
	invalid = append(invalid, bad)
	bad = OrganizationRequest{}
	json.Unmarshal(fixture["request"], &bad)
	bad.EmailIDs = []string{"one", "two"}
	invalid = append(invalid, bad)
	for i, bad := range invalid {
		if bad.Valid() {
			t.Fatalf("unsafe suggestion request %d accepted", i)
		}
	}
}
