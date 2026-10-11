// Package ipc implements the private, bounded host/core JSON-RPC protocol.
package ipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"mailpilot.local/core/internal/domain"
)

const MaxFrameBytes = 64 * 1024

type HealthService interface {
	Health(context.Context) domain.Health
}
type Factory func(context.Context) (HealthService, error)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
type response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

func strictDecode(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}

// Serve handles one bounded request at a time. EOF is a clean host shutdown.
// Bootstrap has no long-running jobs or mutation/cancellation API.
func Serve(ctx context.Context, input io.Reader, output io.Writer, factory Factory) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), MaxFrameBytes)
	encoder := json.NewEncoder(output)
	var service HealthService
	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var req request
		reply := response{JSONRPC: "2.0"}
		fail := func(code int, message string) { reply.Error = &rpcError{Code: code, Message: message} }
		if err := strictDecode(scanner.Bytes(), &req); err != nil || req.JSONRPC != "2.0" || req.ID == "" || len(req.ID) > 64 || req.Method == "" {
			fail(-32600, "Invalid request")
		} else {
			reply.ID = req.ID
			callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			switch req.Method {
			case "initialize":
				var params struct {
					ProtocolVersion int `json:"protocolVersion"`
				}
				if strictDecode(req.Params, &params) != nil || params.ProtocolVersion != domain.ProtocolVersion {
					fail(-32602, "Incompatible protocol version")
				} else if service != nil {
					fail(-32600, "Already initialized")
				} else {
					var err error
					service, err = factory(callCtx)
					if err != nil {
						fail(-32603, "Backend initialization failed")
					} else {
						reply.Result = map[string]int{"protocolVersion": domain.ProtocolVersion}
					}
				}
			case "health", "shutdown":
				if len(req.Params) > 0 && string(req.Params) != "{}" {
					fail(-32602, "Invalid parameters")
				} else if service == nil {
					fail(-32001, "Initialize first")
				} else if req.Method == "health" {
					reply.Result = service.Health(callCtx)
				} else {
					reply.Result = map[string]bool{"stopping": true}
				}
			default:
				fail(-32601, "Method not found")
			}
			cancel()
		}
		if err := encoder.Encode(reply); err != nil {
			return err
		}
		if req.Method == "shutdown" && reply.Error == nil {
			return nil
		}
	}
	return scanner.Err()
}
