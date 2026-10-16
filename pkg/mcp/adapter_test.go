package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/aicapability"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func adapterGrant(t *testing.T, scopes []string) (*aicapability.Manager, aicapability.OAuthTokens, string) {
	t.Helper()
	manager := aicapability.NewManager()
	t.Cleanup(manager.Close)
	client, err := manager.RegisterOAuthClient("example.org", aicapability.OAuthClient{RedirectURIs: []string{"https://client.example/callback"}})
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("v", 43)
	digest := sha256.Sum256([]byte(verifier))
	resource := "https://example.org" + Path
	pairing, err := manager.BeginOAuth("example.org", aicapability.OAuthAuthorization{ClientID: client.ID, RedirectURI: client.RedirectURIs[0], Resource: resource, Challenge: base64.RawURLEncoding.EncodeToString(digest[:]), Scopes: scopes})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.DecidePairing("example.org", "owner@example.org", pairing.VerificationCode, true, scopes); err != nil {
		t.Fatal(err)
	}
	callback, err := manager.OAuthApprovalRedirect("example.org", pairing.VerificationCode)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(callback)
	tokens, err := manager.ExchangeOAuthCode("example.org", client.ID, resource, client.RedirectURIs[0], parsed.Query().Get("code"), verifier)
	if err != nil {
		t.Fatal(err)
	}
	return manager, tokens, resource
}

func TestAdapterRealSDKDiscoveryCallsAndSiteBinding(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25", "2025-06-18"} {
		t.Run(version, func(t *testing.T) {
			manager, tokens, resource := adapterGrant(t, aicapability.StandardScopes())
			calls := make(chan Invocation, 1)
			server := httptest.NewServer(NewHandler(Options{Domain: "example.org", Resource: resource, Version: "test", Bearer: tokens.AccessToken, Manager: manager, Invoke: func(_ context.Context, invocation Invocation) (Result, error) {
				calls <- invocation
				return Result{Status: 200, Payload: map[string]any{"pages": []string{"/", "/about"}}}, nil
			}}))
			defer server.Close()
			boundary, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			client := sdk.NewClient(&sdk.Implementation{Name: "External MCP client", Version: "test"}, nil)
			session, err := client.Connect(boundary, &sdk.StreamableClientTransport{Endpoint: server.URL}, &sdk.ClientSessionOptions{ProtocolVersion: version})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			tools, err := session.ListTools(boundary, &sdk.ListToolsParams{})
			if err != nil {
				t.Fatal(err)
			}
			if len(tools.Tools) != 19 {
				t.Fatal("missing SiteBrush operations", len(tools.Tools))
			}
			for _, tool := range tools.Tools {
				if tool.Annotations == nil || tool.InputSchema == nil || tool.Description == "" {
					t.Fatal("missing tool contract", tool.Name)
				}
			}
			access, err := session.CallTool(boundary, &sdk.CallToolParams{Name: "sitebrush_get_access", Arguments: map[string]any{}})
			if err != nil || access.IsError {
				t.Fatal("access", err)
			}
			encoded, _ := json.Marshal(access)
			if strings.Contains(string(encoded), tokens.AccessToken) || strings.Contains(string(encoded), tokens.RefreshToken) || !strings.Contains(string(encoded), "example.org") {
				t.Fatal("unsafe access response")
			}
			result, err := session.CallTool(boundary, &sdk.CallToolParams{Name: "sitebrush_list_pages", Arguments: map[string]any{}})
			if err != nil || result.IsError {
				t.Fatal("list pages", err)
			}
			select {
			case invocation := <-calls:
				if invocation.Operation != "list_pages" || invocation.Session.Domain != "example.org" {
					t.Fatal("wrong operation binding")
				}
			case <-boundary.Done():
				t.Fatal("operation not invoked")
			}
			invalid, err := session.CallTool(boundary, &sdk.CallToolParams{Name: "sitebrush_update_page", Arguments: map[string]any{"path": "/", "title": "Title", "html": "<p>draft</p>"}})
			if err == nil && !invalid.IsError {
				t.Fatal("missing expected_version passed schema validation")
			}
		})
	}
}

func TestAdapterRevocationAndScopesPreventExecution(t *testing.T) {
	manager, tokens, resource := adapterGrant(t, []string{aicapability.ScopeRead})
	calls := make(chan Invocation, 1)
	server := httptest.NewServer(NewHandler(Options{Domain: "example.org", Resource: resource, Version: "test", Bearer: tokens.AccessToken, Manager: manager, Invoke: func(_ context.Context, invocation Invocation) (Result, error) {
		calls <- invocation
		return Result{Status: 200, Payload: map[string]any{}}, nil
	}}))
	defer server.Close()
	boundary, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "client", Version: "test"}, nil).Connect(boundary, &sdk.StreamableClientTransport{Endpoint: server.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	denied, err := session.CallTool(boundary, &sdk.CallToolParams{Name: "sitebrush_publish", Arguments: map[string]any{}})
	if err != nil || !denied.IsError {
		t.Fatal("missing publish scope was not enforced", err)
	}
	select {
	case <-calls:
		t.Fatal("unauthorized operation reached site owner")
	default:
	}
	_, capability, err := manager.ValidateOAuth("example.org", resource, tokens.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.RevokeCapabilityID(capability.ID, "owner@example.org", "example.org"); err != nil {
		t.Fatal(err)
	}
	revoked, err := session.CallTool(boundary, &sdk.CallToolParams{Name: "sitebrush_get_access", Arguments: map[string]any{}})
	if err != nil || !revoked.IsError {
		t.Fatal("revoked session accepted", err)
	}
}

func TestAdapterAttachmentUsesBytesAndSuppressesDownloadURLs(t *testing.T) {
	manager, tokens, resource := adapterGrant(t, aicapability.StandardScopes())
	transfers := make(chan string, 1)
	calls := make(chan Invocation, 1)
	server := httptest.NewServer(NewHandler(Options{Domain: "example.org", Resource: resource, Version: "test", Bearer: tokens.AccessToken, Manager: manager,
		Download: func(_ context.Context, address string, maximum int64) ([]byte, error) {
			transfers <- address
			if maximum != 128<<20 {
				t.Error("wrong transfer limit")
			}
			return []byte("image bytes"), nil
		},
		Invoke: func(_ context.Context, invocation Invocation) (Result, error) {
			calls <- invocation
			return Result{Status: 200, Payload: map[string]string{"url": "/p/immutable.png"}}, nil
		},
	}))
	defer server.Close()
	boundary, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "client", Version: "test"}, nil).Connect(boundary, &sdk.StreamableClientTransport{Endpoint: server.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	attachment := map[string]any{"file": map[string]string{"download_url": "https://files.example/image?signature=private", "file_id": "file_123", "file_name": "photo.png", "mime_type": "image/png"}}
	result, err := session.CallTool(boundary, &sdk.CallToolParams{Name: "sitebrush_upload_file", Arguments: attachment})
	if err != nil || result.IsError {
		t.Fatal("upload", err)
	}
	invocation := <-calls
	if invocation.Arguments.File != nil || invocation.Arguments.FileName != "photo.png" || string(invocation.Arguments.FileContent) != "image bytes" {
		t.Fatal("file input was not converted to normal uploader bytes")
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "signature=") || strings.Contains(string(encoded), "file_123") {
		t.Fatal("private attachment reference leaked in result")
	}
	<-transfers
	attachment["file_content"] = base64.StdEncoding.EncodeToString([]byte("other"))
	invalid, err := session.CallTool(boundary, &sdk.CallToolParams{Name: "sitebrush_upload_file", Arguments: attachment})
	if err != nil || !invalid.IsError {
		t.Fatal("ambiguous upload accepted", err)
	}
	select {
	case <-transfers:
		t.Fatal("invalid file source initiated network transfer")
	default:
	}
}

func TestAdapterOriginBodyLimitAndErrors(t *testing.T) {
	response := httptest.NewRecorder()
	Challenge(response, "https://example.org", http.StatusUnauthorized, aicapability.StandardScopes())
	if response.Code != 401 || !strings.Contains(response.Header().Get("WWW-Authenticate"), ResourceMetadataPath) {
		t.Fatal("missing standard OAuth discovery challenge")
	}
	if result := DecodeResult(409, []byte("revision conflict")); result.Status != 409 || result.Message != "revision conflict" {
		t.Fatal("lost operation conflict")
	}
	if result := DecodeResult(200, []byte("invalid")); result.Status != 500 {
		t.Fatal("invalid internal response accepted")
	}
}

func TestAdapterRevocationInterruptsAttachmentTransfer(t *testing.T) {
	manager, tokens, resource := adapterGrant(t, aicapability.StandardScopes())
	started := make(chan struct{})
	server := httptest.NewServer(NewHandler(Options{Domain: "example.org", Resource: resource, Version: "test", Bearer: tokens.AccessToken, Manager: manager,
		Download: func(boundary context.Context, _ string, _ int64) ([]byte, error) {
			close(started)
			<-boundary.Done()
			return nil, boundary.Err()
		},
		Invoke: func(_ context.Context, _ Invocation) (Result, error) {
			t.Error("revoked transfer reached the uploader")
			return Result{}, nil
		},
	}))
	defer func() {
		manager.Close()
		server.Close()
	}()
	boundary, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := sdk.NewClient(&sdk.Implementation{Name: "client", Version: "test"}, nil).Connect(boundary, &sdk.StreamableClientTransport{Endpoint: server.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	finished := make(chan bool, 1)
	go func() {
		result, callErr := connection.CallTool(boundary, &sdk.CallToolParams{Name: "sitebrush_upload_file", Arguments: map[string]any{"file": map[string]string{"download_url": "https://files.example/image", "file_id": "file_123"}}})
		finished <- callErr != nil || result.IsError
	}()
	select {
	case <-started:
	case <-boundary.Done():
		t.Fatal("transfer did not start")
	}
	if err := manager.RevokeOAuth("example.org", "invalid-client", tokens.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.ValidateOAuth("example.org", resource, tokens.AccessToken); err != nil {
		t.Fatal("another client revoked the transfer", err)
	}
	grants, err := manager.List("owner@example.org", "example.org")
	if err != nil || len(grants) != 1 {
		t.Fatal("missing capability", err)
	}
	if err := manager.RevokeCapabilityID(grants[0].CapabilityID, "owner@example.org", "example.org"); err != nil {
		t.Fatal(err)
	}
	select {
	case failed := <-finished:
		if !failed {
			t.Fatal("revoked transfer succeeded")
		}
	case <-boundary.Done():
		t.Fatal("revocation did not interrupt the transfer")
	}
}
