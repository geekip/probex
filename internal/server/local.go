package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"probex/internal/protocol"
	"strings"
)

func programDirectory() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(path), nil
}

func booleanArgs(args []string) []string {
	result := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if (arg == "--allow-registration" || arg == "--auto-approve" || arg == "-allow-registration" || arg == "-auto-approve") && i+1 < len(args) && (args[i+1] == "false" || args[i+1] == "true") {
			arg += "=" + args[i+1]
			i++
		}
		result = append(result, arg)
	}
	return result
}

func savedAdminToken(path string, create bool) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && create {
		token := protocol.RandomID()
		file, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if errors.Is(e, os.ErrExist) {
			return savedAdminToken(path, false)
		}
		if e != nil {
			return "", fmt.Errorf("create management token %s: %w", path, e)
		}
		_, e = file.WriteString(token + "\n")
		closeErr := file.Close()
		if e != nil {
			return "", fmt.Errorf("save management token %s: %w", path, e)
		}
		return token, closeErr
	}
	if err != nil {
		return "", fmt.Errorf("read management token %s: %w; use --admin-token or PROBEX_ADMIN_TOKEN", path, err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("management token file %s is empty", path)
	}
	return token, nil
}

func localManagement(base string) bool {
	u, err := url.Parse(base)
	return err == nil && (u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback())
}

func managementClient(base, ca string) (*http.Client, error) {
	local := localManagement(base)
	if local {
		ca = ""
	}
	client, err := protocol.HTTPClient(ca)
	if err != nil {
		return nil, err
	}
	if local {
		transport := client.Transport.(*http.Transport)
		transport.Proxy = nil
		// This exception is limited to management requests routed to loopback.
		transport.TLSClientConfig.InsecureSkipVerify = true
		if u, _ := url.Parse(base); u.Hostname() == "localhost" {
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				_, port, err := net.SplitHostPort(address)
				if err != nil {
					return nil, err
				}
				return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", port))
			}
		}
	}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return client, nil
}

func defaultManagementServer(client *http.Client, base string) (string, error) {
	response, firstErr := client.Get(base + "/healthz")
	if firstErr == nil {
		data, err := io.ReadAll(io.LimitReader(response.Body, 4096))
		response.Body.Close()
		if err != nil {
			return "", err
		}
		if response.StatusCode == http.StatusOK {
			return base, nil
		}
		firstErr = fmt.Errorf("HTTP probe returned %d: %s", response.StatusCode, strings.TrimSpace(string(data)))
		if response.StatusCode != http.StatusBadRequest || !strings.Contains(string(data), "Client sent an HTTP request to an HTTPS server") {
			return "", firstErr
		}
	}
	secure := strings.Replace(base, "http://", "https://", 1)
	response, err := client.Get(secure + "/healthz")
	if err != nil {
		return "", fmt.Errorf("local server connection failed: HTTP: %v; HTTPS: %w", firstErr, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTPS probe returned %d (HTTP: %v)", response.StatusCode, firstErr)
	}
	return secure, nil
}
