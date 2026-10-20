// Package model seals the single supported model's artifact and encoding identity.
// It has no inference, database, network, credentials or executable access.
package model

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"

	"mailpilot.local/core/internal/mail/wordpiece"
)

const Dimensions = 384
const Version = "minilm-1110a243-6fd5d72f-f32-mean-l2-s24-v1-" + wordpiece.NormalizationVersion
const InputPolicy = "wordpiece-cls-sep-subject24-section-v1-no-truncation"

//go:embed manifest.json
var manifestJSON []byte

type Manifest struct {
	ModelID              string `json:"modelId"`
	ModelRevision        string `json:"modelRevision"`
	ModelFile            string `json:"modelFile"`
	ModelBytes           int64  `json:"modelBytes"`
	ModelSHA256          string `json:"modelSha256"`
	ModelURL             string `json:"modelUrl"`
	RuntimeVersion       string `json:"runtimeVersion"`
	RuntimeArchiveBytes  int64  `json:"runtimeArchiveBytes"`
	RuntimeArchiveSHA256 string `json:"runtimeArchiveSha256"`
	RuntimeURL           string `json:"runtimeUrl"`
	LibraryFile          string `json:"libraryFile"`
	LibraryBytes         int64  `json:"libraryBytes"`
	LibrarySHA256        string `json:"librarySha256"`
	LibraryCodeSHA256    string `json:"libraryCodeSha256"`
	MinimumMacOS         string `json:"minimumMacOS"`
	Dimensions           int    `json:"dimensions"`
	Pooling              string `json:"pooling"`
	Normalization        string `json:"normalization"`
	Binding              string `json:"binding"`
}

func Artifacts() Manifest {
	var m Manifest
	if json.Unmarshal(manifestJSON, &m) != nil {
		panic("invalid compiled embedding manifest")
	}
	return m
}

type Descriptor struct {
	ModelID                  string `json:"modelId"`
	Version                  string `json:"version"`
	Dimensions               int    `json:"dimensions"`
	PreprocessingFingerprint string `json:"preprocessingFingerprint"`
	ModelSHA256              string `json:"modelSha256"`
	TokenizerSHA256          string `json:"tokenizerSha256"`
	RuntimeVersion           string `json:"runtimeVersion"`
	SymmetricQueries         bool   `json:"symmetricQueries"`
}

func Identity() Descriptor {
	m := Artifacts()
	sum := sha256.Sum256([]byte(m.ModelID + "\n" + m.ModelSHA256 + "\n" + wordpiece.VocabSHA256 + "\n" + wordpiece.NormalizationVersion + "\n" + InputPolicy + "\n" + m.Pooling + "\n" + m.Normalization + "\n" + m.RuntimeVersion))
	return Descriptor{ModelID: m.ModelID, Version: Version, Dimensions: Dimensions, PreprocessingFingerprint: fmt.Sprintf("%x", sum), ModelSHA256: m.ModelSHA256, TokenizerSHA256: wordpiece.VocabSHA256, RuntimeVersion: m.RuntimeVersion, SymmetricQueries: true}
}
