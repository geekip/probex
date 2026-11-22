package agent

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

func connectionError(err error, customCA bool) error {
	if errors.Is(err, http.ErrSchemeMismatch) {
		return fmt.Errorf("server endpoint does not provide TLS; check the port and use ws:// for a plaintext endpoint: %w", err)
	}
	var unknown x509.UnknownAuthorityError
	if errors.As(err, &unknown) {
		if customCA {
			return fmt.Errorf("CA verification failed: the supplied CA does not trust this server; check --ca and the server certificate chain: %w", err)
		}
		return fmt.Errorf("server certificate is not trusted: a private CA or a missing system root requires --ca /path/to/probex.crt (or a join key containing the CA): %w", err)
	}
	var hostname x509.HostnameError
	if errors.As(err, &hostname) {
		return fmt.Errorf("CA verification failed: server address does not match certificate SANs; use a matching --server address or regenerate the server certificate with --hosts: %w", err)
	}
	var invalid x509.CertificateInvalidError
	if errors.As(err, &invalid) && invalid.Reason == x509.Expired {
		return fmt.Errorf("CA verification failed: certificate expired or not yet valid; check device time and renew the certificate: %w", err)
	}
	var certificate *tls.CertificateVerificationError
	if errors.As(err, &certificate) {
		return fmt.Errorf("CA verification failed; check the CA, certificate chain and device time: %w", err)
	}
	var record tls.RecordHeaderError
	if errors.As(err, &record) {
		return fmt.Errorf("TLS handshake failed: check that --server uses wss:// for a TLS endpoint and the correct port: %w", err)
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return fmt.Errorf("server DNS lookup failed; check --server hostname and DNS configuration: %w", err)
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return fmt.Errorf("server connection timed out; check the address, port and firewall: %w", err)
	}
	var dial *net.OpError
	if errors.As(err, &dial) && dial.Op == "dial" {
		return fmt.Errorf("server connection failed; check --server, the listening address and firewall: %w", err)
	}
	return err
}

func rejectedResponse(response *http.Response) error {
	data, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		return fmt.Errorf("server HTTP %d: read error response: %w", response.StatusCode, err)
	}
	if strings.Contains(string(data), "Client sent an HTTP request to an HTTPS server") {
		return &permanentError{fmt.Errorf("server requires TLS: change --server to wss:// and provide --ca if using a private CA")}
	}
	var reply struct {
		Error     string `json:"error"`
		Admission string `json:"admission"`
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		return responseFailure(response.StatusCode, fmt.Sprintf("unexpected server response; check the server URL and reverse proxy: %v", err))
	}
	if reply.Error == "" && (reply.Admission == "rejected" || reply.Admission == "deleted") {
		reply.Error = "registration " + reply.Admission + "; use --rejoin or a new join key to request admission again"
	}
	return responseFailure(response.StatusCode, reply.Error)
}

func responseFailure(status int, message string) error {
	switch message {
	case "registration disabled":
		message += "; the server does not allow self-registration; request a join key from the administrator"
	case "node name already exists":
		message += "; choose a different --name or ask the administrator to remove the existing node"
	case "invalid node name":
		message += "; use 1–64 bytes containing only letters, digits, hyphens, underscores or dots"
	case "invalid node identity":
		message += "; use a valid state file, --rejoin, or a new join key"
	case "node not registered", "node not found":
		message += "; use --rejoin or request a new join key from this server"
	case "":
		message = "request rejected; check the server URL, registration policy and credentials"
	}
	err := fmt.Errorf("server HTTP %d: %s", status, message)
	if status >= 400 && status < 500 && status != http.StatusTooManyRequests {
		return &permanentError{err}
	}
	return err
}
