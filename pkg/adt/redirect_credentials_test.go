package adt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestCheckRedirect_DoesNotLeakCredentialsOffHost guards the narrowing of the
// header re-attach. The handler exists so a redirect inside the SAP system
// keeps Authorization (issue #90); it must not hand Basic credentials and the
// session CSRF token to an identity provider on another host, which is exactly
// where an expired SSO session redirects to.
// It drives a real redirect through the client rather than calling
// CheckRedirect directly. That distinction is the whole test: net/http copies
// every non-sensitive header onto the next request *before* CheckRedirect runs
// (client.go — copyHeaders then checkRedirect), so a handler that merely
// declines to set X-CSRF-Token off-host leaves Go's copy of it in place and the
// identity provider receives the session token anyway. Calling CheckRedirect
// with a freshly-made empty header map cannot see that, and passed while the
// leak was live.
func TestCheckRedirect_DoesNotLeakCredentialsOffHost(t *testing.T) {
	var idpAuth, idpCSRF, idpSessionType string
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idpAuth = r.Header.Get("Authorization")
		idpCSRF = r.Header.Get("X-CSRF-Token")
		idpSessionType = r.Header.Get("X-sap-adt-sessiontype")
	}))
	defer idp.Close()
	// httptest serves on 127.0.0.1; "localhost" is a different hostname to
	// Go's cross-origin test, which is what makes this a foreign host.
	idpURL := strings.Replace(idp.URL, "127.0.0.1", "localhost", 1)

	var sapAuth, sapCSRF string
	var sap *httptest.Server
	sap = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sap/bc/adt/discovery":
			http.Redirect(w, r, idpURL+"/saml/sso", http.StatusFound)
		case "/sap/bc/adt/hop":
			// Same host, different path — the intra-SAP redirect the handler
			// exists to serve.
			http.Redirect(w, r, sap.URL+"/sap/bc/adt/landed", http.StatusFound)
		default:
			sapAuth = r.Header.Get("Authorization")
			sapCSRF = r.Header.Get("X-CSRF-Token")
		}
	}))
	defer sap.Close()

	cfg := NewConfig(sap.URL, "TESTUSER", "secret")
	client := cfg.NewHTTPClient()

	do := func(path string) {
		t.Helper()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, sap.URL+path, nil)
		if err != nil {
			t.Fatalf("building request: %v", err)
		}
		req.SetBasicAuth("TESTUSER", "secret")
		req.Header.Set("X-CSRF-Token", "tok")
		req.Header.Set("X-sap-adt-sessiontype", "stateful")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
	}

	do("/sap/bc/adt/discovery")
	if idpAuth != "" {
		t.Errorf("the identity provider received Authorization=%q", idpAuth)
	}
	if idpCSRF != "" {
		t.Errorf("the identity provider received X-CSRF-Token=%q — declining to set a "+
			"header is not the same as deleting the copy net/http already made", idpCSRF)
	}
	if idpSessionType != "" {
		t.Errorf("the identity provider received X-sap-adt-sessiontype=%q", idpSessionType)
	}

	do("/sap/bc/adt/hop")
	if sapAuth == "" {
		t.Error("an intra-SAP redirect lost Authorization — issue #90 is what this handler is for")
	}
	if sapCSRF != "tok" {
		t.Errorf("an intra-SAP redirect carried X-CSRF-Token=%q, want tok — the lock→write "+
			"sequence needs it on the second hop", sapCSRF)
	}
}

// TestCheckRedirect_HostMatchIgnoresCaseAndPort pins the comparison itself.
// `req.URL.Host == sapHost` treated an ICM redirect that merely changed the
// case of the FQDN, or spelled out :443, as a hop to a foreign host — which
// silently dropped the very headers the handler exists to preserve. The port is
// ignored outright, because the off-host branch now *deletes* those headers and
// must not be stricter than net/http's own rule, which compares hostnames only:
// one box answering on two ports is not a credential boundary.
func TestCheckRedirect_HostMatchIgnoresCaseAndPort(t *testing.T) {
	cfg := NewConfig("https://SAPDEV.example.com", "TESTUSER", "secret")
	client := cfg.NewHTTPClient()

	first, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://SAPDEV.example.com/sap/bc/adt/discovery", nil)
	first.SetBasicAuth("TESTUSER", "secret")
	first.Header.Set("X-CSRF-Token", "tok")

	for _, tc := range []struct {
		name    string
		target  string
		wantSet bool
	}{
		{"case-differing FQDN is the same host", "https://sapdev.example.com/sap/bc/adt/other", true},
		{"explicit default port is the same host", "https://sapdev.example.com:443/sap/bc/adt/other", true},
		{"another port on the same box is the same host", "https://sapdev.example.com:8443/sap/bc/adt/other", true},
		{"foreign idp is not", "https://idp.example.org/saml/sso", false},
		{"a sibling subdomain is not", "https://idp.sapdev.example.com/saml/sso", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, tc.target, nil)
			if err := client.CheckRedirect(next, []*http.Request{first}); err != nil {
				t.Fatalf("CheckRedirect: %v", err)
			}
			gotAuth := next.Header.Get("Authorization") != ""
			gotCSRF := next.Header.Get("X-CSRF-Token") != ""
			if gotAuth != tc.wantSet || gotCSRF != tc.wantSet {
				t.Errorf("target %s: Authorization set=%v, X-CSRF-Token set=%v, want both %v",
					tc.target, gotAuth, gotCSRF, tc.wantSet)
			}
		})
	}
}

// TestCheckRedirect_DoesNotDowngradeScheme pins the half of the rule the host
// comparison cannot see: a hop to the same host over plain http would send the
// credentials and the CSRF token in clear text.
func TestCheckRedirect_DoesNotDowngradeScheme(t *testing.T) {
	for _, tc := range []struct {
		name    string
		base    string
		target  string
		wantSet bool
	}{
		{"https to http on the same host drops them", "https://sapdev.example.com", "http://sapdev.example.com:8000/sap/bc/adt/x", false},
		{"https to http on the same port drops them", "https://sapdev.example.com:44300", "http://sapdev.example.com:44300/x", false},
		{"http to https on another port keeps them", "http://sapdev.example.com:8000", "https://sapdev.example.com:44300/sap/bc/adt/x", true},
		{"http to http on the same host keeps them", "http://sapdev.example.com:8000", "http://sapdev.example.com:8001/x", true},
		{"https to https on another port keeps them", "https://sapdev.example.com:44300", "https://sapdev.example.com:8443/x", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := NewConfig(tc.base, "TESTUSER", "secret").NewHTTPClient()
			first, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, tc.base+"/sap/bc/adt/discovery", nil)
			first.SetBasicAuth("TESTUSER", "secret")
			first.Header.Set("X-CSRF-Token", "tok")
			first.Header.Set("X-sap-adt-sessiontype", "stateful")

			next, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, tc.target, nil)
			// What net/http's own copier has already done before CheckRedirect runs.
			next.Header.Set("X-CSRF-Token", "tok")
			next.Header.Set("X-sap-adt-sessiontype", "stateful")
			if err := client.CheckRedirect(next, []*http.Request{first}); err != nil {
				t.Fatalf("CheckRedirect: %v", err)
			}
			for _, h := range []string{"Authorization", "X-CSRF-Token", "X-sap-adt-sessiontype"} {
				if got := next.Header.Get(h) != ""; got != tc.wantSet {
					t.Errorf("%s set=%v, want %v", h, got, tc.wantSet)
				}
			}
		})
	}
}

func TestKeepsSAPCredentials(t *testing.T) {
	parse := func(s string) *url.URL {
		u, err := url.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	for _, tc := range []struct {
		name         string
		base, target string
		want         bool
	}{
		{"same origin", "https://sap.example.com", "https://sap.example.com/x", true},
		{"case-folded host", "https://SAP.example.com", "https://sap.example.com/x", true},
		{"explicit default port", "https://sap.example.com", "https://sap.example.com:443/x", true},
		{"foreign host", "https://sap.example.com", "https://idp.example.org/x", false},
		{"downgrade", "https://sap.example.com", "http://sap.example.com/x", false},
		{"upgrade", "http://sap.example.com:8000", "https://sap.example.com:44300/x", true},
		{"scheme-less BaseURL has no host", "sap.example.com", "https://sap.example.com/x", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := keepsSAPCredentials(parse(tc.base), parse(tc.target)); got != tc.want {
				t.Errorf("keepsSAPCredentials(%s, %s) = %v, want %v", tc.base, tc.target, got, tc.want)
			}
		})
	}
	if keepsSAPCredentials(nil, parse("https://sap.example.com")) {
		t.Error("nil base keeps credentials")
	}
}
