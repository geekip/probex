package server

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestGeneratedTLSAndCAExport(t *testing.T) {
	dir := t.TempDir()
	cert, key, ca := filepath.Join(dir, "server.cert"), filepath.Join(dir, "server.key"), filepath.Join(dir, "root.crt")
	args := []string{"--tls-cert", cert, "--tls-key", key, "--ca", ca}
	if err := certificateCommand("create-tls", append(args, "--hosts", "localhost,127.0.0.1,::1,probex.example.com")); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(cert)
	if err != nil {
		t.Fatal(err)
	}
	if err := certificateCommand("create-tls", args); err == nil {
		t.Fatal("existing files overwritten")
	}
	after, err := os.ReadFile(cert)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("existing certificate modified", err)
	}
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	rootData, err := os.ReadFile(ca)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(rootData) {
		t.Fatal("invalid root")
	}
	for _, host := range []string{"localhost", "127.0.0.1", "::1", "probex.example.com"} {
		if _, err := pair.Leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: host}); err != nil {
			t.Fatal(host, err)
		}
	}
	if err := pair.Leaf.VerifyHostname("wrong.example.com"); err == nil {
		t.Fatal("wrong hostname accepted")
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{pair}}
	server.StartTLS()
	defer server.Close()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}}
	defer client.CloseIdleConnections()
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal("generated TLS handshake", err)
	}
	response.Body.Close()
	exported, err := exportCA(pair.Certificate)
	if err != nil || !bytes.Equal(exported, rootData) {
		t.Fatal("exported CA differs", err)
	}
	if _, err := exportCA(pair.Certificate[:1]); err == nil {
		t.Fatal("fabricated CA for leaf certificate")
	}
	if _, _, _, err := generateTLS("https://example.com"); err == nil {
		t.Fatal("URL accepted as SAN")
	}
	if _, _, _, err := generateTLS(""); err == nil {
		t.Fatal("empty SAN list accepted")
	}
	if err := certificateCommand("create-ca", []string{"--tls-cert", cert, "--tls-key", key, "--ca", filepath.Join(dir, "export.crt")}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(key)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	otherChain, otherKey, _, err := generateTLS("localhost")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.key"), otherKey, 0600); err != nil {
		t.Fatal(err)
	}
	if err := certificateCommand("create-ca", []string{"--tls-cert", cert, "--tls-key", filepath.Join(dir, "other.key"), "--ca", filepath.Join(dir, "bad.crt")}); err == nil {
		t.Fatal("mismatched private key accepted")
	}
	block, _ := pem.Decode(otherChain)
	unrelated, _ := pem.Decode(rootData)
	if _, err := exportCA([][]byte{block.Bytes, unrelated.Bytes}); err == nil {
		t.Fatal("unrelated CA accepted")
	}
	selfSignedServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer selfSignedServer.Close()
	selfSigned := selfSignedServer.Certificate()
	// The httptest certificate is self-signed; no issuer private key is needed to export it.
	if _, err := exportCA([][]byte{selfSigned.Raw}); err != nil {
		t.Fatal("self-signed certificate export", err)
	}
}
