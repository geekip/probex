package agent

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"probex/internal/files"
	"probex/internal/tunnel"
	"sync"
)

// Keep the tunnel readable until the request body has been consumed, including
// when the loopback service rejects headers before reading the upload body.
type fileRequestBody struct {
	io.ReadCloser
	once sync.Once
	err  error
}

func (b *fileRequestBody) Close() error {
	b.once.Do(func() {
		_, readErr := io.Copy(io.Discard, b.ReadCloser)
		b.err = errors.Join(readErr, b.ReadCloser.Close())
	})
	return b.err
}
func (s *session) proxyFile(ctx context.Context, stream *tunnel.Stream) error {
	defer stream.Close()
	s.fileMu.Lock()
	if s.fileService == nil {
		service, err := files.New()
		if err != nil {
			s.fileMu.Unlock()
			return err
		}
		s.fileService = service
	}
	service := s.fileService
	s.fileMu.Unlock()
	req, err := http.ReadRequest(bufio.NewReader(stream))
	if err != nil {
		return err
	}
	req.Body = &fileRequestBody{ReadCloser: req.Body}
	defer req.Body.Close()
	req = req.WithContext(ctx)
	req.RequestURI = ""
	req.URL.Scheme = "http"
	req.URL.Host = service.Address
	req.Host = service.Address
	req.Close = true
	req.Header.Set("Authorization", "Bearer "+service.Key)
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	response, err := transport.RoundTrip(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if err := req.Body.Close(); err != nil {
		return err
	}
	response.Close = true
	return response.Write(stream)
}
