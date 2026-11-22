package protocol

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Enrollment struct {
	Version   int       `json:"v"`
	Port      int       `json:"port"`
	NodeID    string    `json:"node_id"`
	Name      string    `json:"name"`
	Token     string    `json:"token"`
	CA        string    `json:"ca_pem,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (e Enrollment) Encode() (string, error) {
	data, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	return "px1_" + base64.RawURLEncoding.EncodeToString(data), nil
}

func DecodeEnrollment(key string) (Enrollment, error) {
	var e Enrollment
	if len(key) > MaxMessage || !strings.HasPrefix(key, "px1_") {
		return e, fmt.Errorf("invalid join key format")
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(key, "px1_"))
	if err != nil {
		return e, fmt.Errorf("invalid join key encoding")
	}
	if err := json.Unmarshal(data, &e); err != nil || e.Version != 1 || e.Name == "" || e.Port < 0 || e.Port > 65535 || !ValidNodeID(e.NodeID) {
		return e, fmt.Errorf("invalid join key payload")
	}
	for _, value := range []string{e.Token} {
		b, err := hex.DecodeString(value)
		if err != nil || len(b) != 32 {
			return e, fmt.Errorf("invalid join key credentials")
		}
	}
	return e, nil
}
