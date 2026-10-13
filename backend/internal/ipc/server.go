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

	"mailpilot.local/core/internal/app"
	"mailpilot.local/core/internal/auth"
	"mailpilot.local/core/internal/domain"
	mailsync "mailpilot.local/core/internal/sync"
)

const MaxFrameBytes = 64 * 1024

type HealthService interface {
	Health(context.Context) domain.Health
}
type AuthService interface {
	AuthCancel() auth.Status
	AuthStatus() auth.Status
	AuthBegin() auth.BeginResult
	AuthCheck() auth.Status
	AuthDisconnect() auth.Status
}
type ReaderService interface {
	ReadMail(context.Context, app.ReadMailRequest) app.ReadMailResponse
}
type SyncCanceller interface{ SyncCancel() mailsync.Progress }
type MailService interface {
	SyncStart() mailsync.Progress
	SyncStatus() mailsync.Progress
	Mailbox(context.Context, int) app.MailboxPage
}
type CategoryService interface {
	ManageCategories(context.Context, app.CategoryRequest) app.CategoryPage
}
type AskService interface {
	AskMail(context.Context, app.AskRequest) app.AskResponse
}

type ReplyService interface {
	Reply(context.Context, app.ReplyRequest) app.ReplyResponse
}
type SearchService interface {
	SearchMailbox(context.Context, app.SearchRequest) app.SearchResponse
}

type ClassificationService interface {
	ReviewClassifications(context.Context, app.ClassificationRequest) app.ClassificationPage
}
type OrganizationService interface {
	Organize(context.Context, app.OrganizationRequest) app.OrganizationResponse
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
// Authentication methods start background work and return immediately.
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
			timeout := 5 * time.Second
			if req.Method == "classification.review" {
				timeout = 30 * time.Second
			}
			callCtx, cancel := context.WithTimeout(ctx, timeout)
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
			case "organization.manage":
				organization, ok := service.(OrganizationService)
				var params app.OrganizationRequest
				if strictDecode(req.Params, &params) != nil || !params.Valid() {
					fail(-32602, "Invalid parameters")
				} else if !ok {
					fail(-32001, "Organization unavailable")
				} else {
					reply.Result = organization.Organize(callCtx, params)
				}
			case "ask.manage":
				var params app.AskRequest
				ask, ok := service.(AskService)
				if !ok {
					fail(-32601, "Method not found")
				} else if strictDecode(req.Params, &params) != nil || !params.Valid() {
					fail(-32602, "Invalid parameters")
				} else {
					reply.Result = ask.AskMail(callCtx, params)
				}
			case "reply.manage":
				var params app.ReplyRequest
				replies, ok := service.(ReplyService)
				if strictDecode(req.Params, &params) != nil || !params.Valid() {
					fail(-32602, "Invalid parameters")
				} else if !ok {
					fail(-32001, "Replies unavailable")
				} else {
					reply.Result = replies.Reply(callCtx, params)
				}
			case "search.semantic":
				var params app.SearchRequest
				search, ok := service.(SearchService)
				if strictDecode(req.Params, &params) != nil || !params.Valid() {
					fail(-32602, "Invalid parameters")
				} else if !ok {
					fail(-32001, "Search unavailable")
				} else {
					reply.Result = search.SearchMailbox(callCtx, params)
				}

			case "classification.review":
				review, ok := service.(ClassificationService)
				var params app.ClassificationRequest
				if strictDecode(req.Params, &params) != nil || !params.Valid() {
					fail(-32602, "Invalid parameters")
				} else if !ok {
					fail(-32001, "Classification unavailable")
				} else {
					reply.Result = review.ReviewClassifications(callCtx, params)
				}
			case "categories.manage":
				categories, ok := service.(CategoryService)
				var params app.CategoryRequest
				if strictDecode(req.Params, &params) != nil || !params.Valid() {
					fail(-32602, "Invalid parameters")
				} else if !ok {
					fail(-32001, "Categories unavailable")
				} else {
					reply.Result = categories.ManageCategories(callCtx, params)
				}
			case "mail.read":
				var params app.ReadMailRequest
				reader, ok := service.(ReaderService)
				if strictDecode(req.Params, &params) != nil || !params.Valid() {
					fail(-32602, "Invalid parameters")
				} else if !ok {
					fail(-32001, "Reader unavailable")
				} else {
					reply.Result = reader.ReadMail(callCtx, params)
				}
			case "sync.cancel":
				canceller, ok := service.(SyncCanceller)
				if len(req.Params) > 0 && string(req.Params) != "{}" {
					fail(-32602, "Invalid parameters")
				} else if !ok {
					fail(-32001, "Sync unavailable")
				} else {
					reply.Result = canceller.SyncCancel()
				}
			case "sync.start", "sync.status", "mail.list":
				mailbox, ok := service.(MailService)
				if !ok {
					fail(-32001, "Mailbox unavailable")
					break
				}
				if req.Method == "mail.list" {
					var params struct {
						Offset int `json:"offset"`
					}
					if strictDecode(req.Params, &params) != nil || params.Offset < 0 || params.Offset > 1000000 {
						fail(-32602, "Invalid parameters")
					} else {
						reply.Result = mailbox.Mailbox(callCtx, params.Offset)
					}
				} else if len(req.Params) > 0 && string(req.Params) != "{}" {
					fail(-32602, "Invalid parameters")
				} else if req.Method == "sync.start" {
					reply.Result = mailbox.SyncStart()
				} else {
					reply.Result = mailbox.SyncStatus()
				}
			case "auth.status", "auth.begin", "auth.check", "auth.disconnect", "auth.cancel":
				account, ok := service.(AuthService)
				if len(req.Params) > 0 && string(req.Params) != "{}" {
					fail(-32602, "Invalid parameters")
				} else if !ok {
					fail(-32001, "Authentication unavailable")
				} else {
					switch req.Method {
					case "auth.cancel":
						reply.Result = account.AuthCancel()
					case "auth.status":
						reply.Result = account.AuthStatus()
					case "auth.begin":
						reply.Result = account.AuthBegin()
					case "auth.check":
						reply.Result = account.AuthCheck()
					case "auth.disconnect":
						reply.Result = account.AuthDisconnect()
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
