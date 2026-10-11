// Package domain contains transport-independent application values.
package domain

const ProtocolVersion = 1

type Health struct {
	Name            string `json:"name"`
	Version         string `json:"version"`
	ProtocolVersion int    `json:"protocolVersion"`
	Backend         string `json:"backend"`
	Database        string `json:"database"`
	SchemaVersion   int    `json:"schemaVersion"`
}
