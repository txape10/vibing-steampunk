package adt

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// A retry runs right after SAP opened a new session, so the token and the
// session id on the retry's response are the ones the next request needs.
func TestRetryRequest_RemembersTokenAndSessionFromTheRetry(t *testing.T) {
	retryResp := newMockResponse(200, "Success", map[string]string{"X-CSRF-Token": "retry-token"})
	retryResp.Header.Add("Set-Cookie", "sap-contextid=ctx-after-retry; Path=/")
	mock := &mockHTTPClient{
		responses: []*http.Response{
			newMockResponse(200, "OK", map[string]string{"X-CSRF-Token": "old-token"}),
			newMockResponse(403, "Forbidden", nil),
			newMockResponse(200, "OK", map[string]string{"X-CSRF-Token": "refreshed-token"}),
			retryResp,
		},
	}
	transport := NewTransportWithClient(NewConfig("https://sap.example.com:44300", "user", "pass"), mock)

	if _, err := transport.Request(context.Background(), "/sap/bc/adt/test", &RequestOptions{Method: http.MethodPost}); err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	if got := transport.getCSRFToken(); got != "retry-token" {
		t.Errorf("CSRF token after retry = %q, want the retry response's retry-token", got)
	}
	if got := transport.getSessionID(); got != "ctx-after-retry" {
		t.Errorf("session id after retry = %q, want ctx-after-retry", got)
	}
}

// When the token refresh after a 403 fails too, SAP's answer to the write must
// survive: lock-conflict detection reads the message key out of it.
func TestRequest_403WithFailedRefresh_KeepsSAPAnswer(t *testing.T) {
	body := `<exc:exception><message>El usuario X ya está tratando Y</message>` +
		`<properties><entry key="T100KEY-ID">EU</entry><entry key="T100KEY-NO">510</entry></properties></exc:exception>`
	mock := &mockHTTPClient{
		responses: []*http.Response{
			newMockResponse(200, "OK", map[string]string{"X-CSRF-Token": "tok"}),
			newMockResponse(403, body, nil),
			// the refresh (HEAD, then GET fallback) is refused as well
			newMockResponse(500, "boom", nil),
			newMockResponse(500, "boom", nil),
			newMockResponse(500, "boom", nil),
		},
	}
	transport := NewTransportWithClient(NewConfig("https://sap.example.com:44300", "user", "pass"), mock)

	_, err := transport.Request(context.Background(), "/sap/bc/adt/oo/classes/zcl_x", &RequestOptions{Method: http.MethodPost})
	if err == nil {
		t.Fatal("expected an error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusForbidden {
		t.Fatalf("errors.As did not find the 403 APIError in %v", err)
	}
	if !strings.Contains(apiErr.Message, "EU") || !isLockConflictError(err) {
		t.Errorf("SAP's answer was lost: isLockConflictError=%v, message=%q", isLockConflictError(err), apiErr.Message)
	}
}
