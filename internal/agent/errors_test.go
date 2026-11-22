package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConnectionDiagnostics(t *testing.T) {
	for _, test := range []struct {
		err  error
		ca   bool
		want string
	}{
		{x509.UnknownAuthorityError{}, false, "requires --ca"},
		{x509.UnknownAuthorityError{}, true, "supplied CA does not trust"},
		{x509.HostnameError{Certificate: &x509.Certificate{}, Host: "wrong"}, true, "SANs"},
		{x509.CertificateInvalidError{Cert: &x509.Certificate{}, Reason: x509.Expired}, true, "device time"},
		{&net.DNSError{Err: "no such host", Name: "example.invalid"}, false, "DNS lookup failed"},
		{&net.OpError{Op: "dial", Err: errors.New("connection refused")}, false, "server connection failed"},
	} {
		err := connectionError(fmt.Errorf("dial: %w", test.err), test.ca)
		if !strings.Contains(err.Error(), test.want) || !errors.Is(err, test.err) {
			t.Fatal(test.want, err)
		}
	}
}

func TestRegistrationDiagnostics(t *testing.T) {
	for _, test := range []struct {
		status int
		body   string
		want   string
	}{
		{403, `{"error":"registration disabled"}`, "does not allow self-registration"},
		{409, `{"error":"node name already exists"}`, "different --name"},
		{400, `{"error":"invalid node name"}`, "1–64 bytes"},
		{403, `{"admission":"rejected"}`, "--rejoin"},
		{403, `{"admission":"deleted"}`, "--rejoin"},
		{400, "Client sent an HTTP request to an HTTPS server.\n", "requires TLS"},
		{404, "<html>Wrong proxy location</html>", "reverse proxy"},
		{403, `{"error":`, "unexpected server response"},
	} {
		t.Run(test.want, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				if strings.HasPrefix(test.body, "Client") || strings.HasPrefix(test.body, "<") {
					w.Header().Set("Content-Type", "text/plain")
				}
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			err := Run(context.Background(), Config{Server: server.URL, Name: "node", State: filepath.Join(t.TempDir(), "state.json")})
			var permanent *permanentError
			if !errors.As(err, &permanent) || !strings.Contains(err.Error(), test.want) {
				t.Fatal("not actionable or did not stop", err)
			}
		})
	}
}

func TestTLSProtocolMismatchStops(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer server.Close()
	err := Run(context.Background(), Config{Server: strings.Replace(server.URL, "http://", "wss://", 1), Name: "node", State: filepath.Join(t.TempDir(), "state.json")})
	if err == nil || !strings.Contains(err.Error(), "does not provide TLS") {
		t.Fatal("TLS mismatch did not stop with actionable error", err)
	}
}

func TestTLSMissingAndValidCADiagnostics(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(403)
		fmt.Fprint(w, `{"error":"registration disabled"}`)
	}))
	defer server.Close()
	config := Config{Server: server.URL, Name: "node", State: filepath.Join(t.TempDir(), "state.json")}
	err := Run(context.Background(), config)
	var verification *tls.CertificateVerificationError
	if !errors.As(err, &verification) || !strings.Contains(err.Error(), "requires --ca") {
		t.Fatal("missing CA diagnostic", err)
	}
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0644); err != nil {
		t.Fatal(err)
	}
	config.CA = ca
	err = Run(context.Background(), config)
	if err == nil || !strings.Contains(err.Error(), "does not allow self-registration") {
		t.Fatal("valid CA did not reach registration", err)
	}
}
