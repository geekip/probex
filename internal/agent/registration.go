package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"probex/internal/protocol"
	"strings"
	"time"
)

func awaitApproval(ctx context.Context, c Config, id *identity, client *http.Client) error {
	base, err := protocol.Endpoint(c.Server, false)
	if err != nil {
		return err
	}
	base = strings.TrimSuffix(base, "/agent")
	info := Collect(ctx)
	body, err := json.Marshal(struct {
		Name string        `json:"name"`
		Info protocol.Info `json:"info"`
	}{c.Name, info})
	if err != nil {
		return err
	}
	method, path := http.MethodPost, "/register"
	for {
		requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		req, err := http.NewRequestWithContext(requestCtx, method, base+path, bytes.NewReader(body))
		if err != nil {
			cancel()
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Probex-ID", id.ID)
		req.Header.Set("Authorization", "Bearer "+id.Key)
		response, err := client.Do(req)
		if err != nil {
			cancel()
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("registration request: %w", err)
		}
		if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusAccepted {
			err := rejectedResponse(response)
			response.Body.Close()
			cancel()
			return err
		}
		var node protocol.Node
		err = json.NewDecoder(response.Body).Decode(&node)
		response.Body.Close()
		cancel()
		if err != nil {
			return fmt.Errorf("registration response: %w", err)
		}
		if !protocol.ValidNodeID(node.ID) {
			return &permanentError{fmt.Errorf("invalid server node ID")}
		}
		if node.Admission != "approved" && node.Admission != "review" {
			return &permanentError{fmt.Errorf("registration %s", node.Admission)}
		}
		id.ID = node.ID
		id.Enrolled = node.Admission == "approved"
		saved, err := json.Marshal(id)
		if err != nil {
			return &permanentError{err}
		}
		if err := os.WriteFile(c.State, saved, 0600); err != nil {
			return &permanentError{fmt.Errorf("save registration identity %s: %w", c.State, err)}
		}
		if id.Enrolled {
			return nil
		}

		if method == http.MethodPost {
			log.Printf("registration submitted for %s (%s); awaiting approval", c.Name, id.ID)
		}
		method, path, body = http.MethodGet, "/registration", nil
		timer := time.NewTimer(3 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}
