package panel

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lattix/backend/internal/extsub"
	"lattix/backend/internal/store"
	"lattix/shared/requester"
)

func newExternalSubscriptionTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("subscription-userinfo", "upload=1; download=2; total=3")
		link := "vless://11111111-2222-3333-4444-555555555555@example.com:443?type=tcp#Node1"
		w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(link))))
	}))
	t.Cleanup(upstream.Close)
	server := &Server{st: st, extSubs: extsub.New(st, upstreamClient(upstream), upstreamClient(upstream))}
	return server, upstream
}

func upstreamClient(upstream *httptest.Server) requester.ExternalFileRequester {
	return requester.ExternalFileRequester{Doer: &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
			},
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}}
}

func TestExternalSubscriptionCreateSyncListDelete(t *testing.T) {
	server, _ := newExternalSubscriptionTestServer(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/external-subscription/create", strings.NewReader(fmt.Sprintf(
		`{"name":"机场","url":%q,"auto_update":true,"update_interval_hours":12}`, "https://sub.example.com/a?token=1")))
	server.handleCreateExternalSubscription(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Code string                     `json:"code"`
		Data store.ExternalSubscription `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Code != "OK" || created.Data.NodeCount != 1 || created.Data.Total != 3 {
		t.Fatalf("created = %+v", created)
	}

	rec = httptest.NewRecorder()
	server.handleListExternalSubscriptions(rec, httptest.NewRequest(http.MethodGet, "/api/external-subscription/list", nil))
	var listed struct {
		Code string                       `json:"code"`
		Data []store.ExternalSubscription `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Data) != 1 || listed.Data[0].ID != created.Data.ID {
		t.Fatalf("listed = %+v", listed)
	}

	rec = httptest.NewRecorder()
	server.handleListExternalChains(rec, httptest.NewRequest(http.MethodGet,
		"/api/external-subscription/chains?id="+fmt.Sprint(created.Data.ID), nil))
	var chains struct {
		Code string                `json:"code"`
		Data []store.ExternalChain `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&chains); err != nil {
		t.Fatal(err)
	}
	if len(chains.Data) != 1 || chains.Data[0].Name != "Node1" {
		t.Fatalf("chains = %+v", chains)
	}

	rec = httptest.NewRecorder()
	server.handleDeleteExternalSubscription(rec, httptest.NewRequest(http.MethodPost,
		"/api/external-subscription/delete",
		strings.NewReader(fmt.Sprintf(`{"id":%d}`, created.Data.ID))))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestExternalSubscriptionEmptyListJSONIsArray(t *testing.T) {
	server, _ := newExternalSubscriptionTestServer(t)

	rec := httptest.NewRecorder()
	server.handleListExternalSubscriptions(rec, httptest.NewRequest(http.MethodGet, "/api/external-subscription/list", nil))
	if strings.Contains(rec.Body.String(), `"data":null`) {
		t.Fatalf("list data must be [] when empty, got: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	server.handleListExternalChains(rec, httptest.NewRequest(http.MethodGet, "/api/external-subscription/chains?id=1", nil))
	if strings.Contains(rec.Body.String(), `"data":null`) {
		t.Fatalf("chains data must be [] when empty, got: %s", rec.Body.String())
	}
}

func TestExternalSubscriptionCreateValidation(t *testing.T) {
	server, _ := newExternalSubscriptionTestServer(t)
	rec := httptest.NewRecorder()
	server.handleCreateExternalSubscription(rec, httptest.NewRequest(http.MethodPost,
		"/api/external-subscription/create", strings.NewReader(`{"name":"x","url":"http://bad.example.com/a"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("validation should return RPC error with 200: %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Code != "INVALID_ARGUMENT" {
		t.Fatalf("code = %q", got.Code)
	}
}
