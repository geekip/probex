package server

import (
	"bytes"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestSessionCookieTransportAndLogout(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	app, err := New(store, Config{AdminToken: "test-admin"})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	for _, test := range []struct {
		name, remote, forwarded string
		tls, secure             bool
	}{
		{"direct HTTP", "192.0.2.1:1234", "", false, false},
		{"direct HTTPS", "192.0.2.1:1234", "", true, true},
		{"local HTTPS proxy", "127.0.0.1:1234", "https", false, true},
		{"untrusted forwarded header", "192.0.2.1:1234", "https", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, "http://probex.test"+path, bytes.NewBufferString(body))
				r.RemoteAddr = test.remote
				r.Header.Set("X-Forwarded-Proto", test.forwarded)
				r.Header.Set("Origin", "http://probex.test")
				if test.tls {
					r.TLS = &tls.ConnectionState{}
				}
				if cookie != nil {
					r.AddCookie(cookie)
				}
				w := httptest.NewRecorder()
				app.Handler().ServeHTTP(w, r)
				return w
			}
			login := request("POST", "/api/login", `{"token":"test-admin"}`, nil)
			if login.Code != 200 {
				t.Fatal(login.Code, login.Body.String())
			}
			cookies := login.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatal("missing session cookie", cookies)
			}
			cookie := cookies[0]
			wantName, otherName := "probex_session_http", "probex_session"
			if test.secure {
				wantName, otherName = otherName, wantName
			}
			if cookie.Name != wantName || cookie.Secure != test.secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.MaxAge != 43200 {
				t.Fatal("incorrect cookie attributes", cookie)
			}
			if got := request("GET", "/api/nodes", "", cookie); got.Code != 200 {
				t.Fatal("valid session rejected", got.Code, got.Body.String())
			}
			if got := request("GET", "/api/nodes", "", &http.Cookie{Name: otherName, Value: cookie.Value}); got.Code != 401 {
				t.Fatal("cookie for other transport accepted", got.Code)
			}
			logout := request("POST", "/api/logout", "", cookie)
			if logout.Code != 200 {
				t.Fatal("logout failed", logout.Code)
			}
			removed := logout.Result().Cookies()
			if len(removed) != 1 || removed[0].Name != cookie.Name || removed[0].MaxAge != -1 || removed[0].Secure != cookie.Secure {
				t.Fatal("logout did not remove the matching cookie", removed)
			}
			if got := request("GET", "/api/nodes", "", cookie); got.Code != 401 {
				t.Fatal("logout did not revoke session", got.Code)
			}
		})
	}
}
