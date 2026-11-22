package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBooleanArgs(t *testing.T) {
	args := []string{"--allow-registration", "false", "--auto-approve", "false", "--db", "test.db", "--auto-approve=true"}
	want := []string{"--allow-registration=false", "--auto-approve=false", "--db", "test.db", "--auto-approve=true"}
	if got := booleanArgs(args); !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
}

func TestSavedAdminToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probexd.token")
	if _, err := savedAdminToken(path, false); err == nil {
		t.Fatal("management command created a token")
	}
	token, err := savedAdminToken(path, true)
	if err != nil || !validKey(token) {
		t.Fatal(token, err)
	}
	for _, create := range []bool{true, false} {
		got, err := savedAdminToken(path, create)
		if err != nil || got != token {
			t.Fatal("token changed", got, err)
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := savedAdminToken(path, true); err == nil {
		t.Fatal("silently replaced an empty existing token")
	}
}

func TestLocalManagementProtocolAndTrust(t *testing.T) {
	health := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("protocol probe exposed credentials")
		}
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	for _, secure := range []bool{false, true} {
		var server *httptest.Server
		if secure {
			server = httptest.NewTLSServer(health)
		} else {
			server = httptest.NewServer(health)
		}
		base := strings.Replace(server.URL, "https://", "http://", 1)
		client, err := managementClient(base, "/missing/ignored-local-ca")
		if err != nil {
			t.Fatal(err)
		}
		client.Timeout = 2 * time.Second
		got, err := defaultManagementServer(client, base)
		if err != nil || got != server.URL {
			t.Fatal(got, server.URL, err)
		}
		client.CloseIdleConnections()
		server.Close()
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		problem(w, 503, "unavailable")
	}))
	defer server.Close()
	client, err := managementClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := defaultManagementServer(client, server.URL); err == nil || !strings.Contains(err.Error(), "503") || requests != 1 {
		t.Fatal("business failure caused protocol fallback", requests, err)
	}
	for _, base := range []string{"https://probex.example.com", "https://127.0.0.1.example.com", "https://10.0.0.2"} {
		client, err := managementClient(base, "")
		if err != nil || client.Transport.(*http.Transport).TLSClientConfig.InsecureSkipVerify {
			t.Fatal("remote TLS verification disabled", base, err)
		}
		if _, err := managementClient(base, "/missing/required-remote-ca"); err == nil {
			t.Fatal("remote CA ignored", base)
		}
	}
}

func TestLocalManagementRejectsRedirect(t *testing.T) {
	reached := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	client, err := managementClient(redirect.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(redirect.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 302 || reached {
		t.Fatal("management request followed redirect")
	}
}
