package protocol

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
)

func HTTPClient(caFile string) (*http.Client, error) {
	var pem []byte
	if caFile != "" {
		var err error
		pem, err = os.ReadFile(caFile)
		if err != nil {
			return nil, err
		}
	}
	return HTTPClientWithCA(pem)
}

func HTTPClientWithCA(pem []byte) (*http.Client, error) {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if len(pem) != 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("CA file contains no certificates")
		}
		t.TLSClientConfig.RootCAs = pool
	}
	return &http.Client{
		Transport:     t,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("remote command exited with code %d", e.Code) }
