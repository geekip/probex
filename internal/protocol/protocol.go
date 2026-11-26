package protocol

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const Version = "0.2.0"
const WireVersion = 5
const MaxMessage = 64 << 10
const MaxOutput = 256 << 10
const ChunkSize = 8 << 10
const MaxTerminals = 8

type Info struct {
	Hostname        string            `json:"hostname"`
	OS              string            `json:"os"`
	Platform        string            `json:"platform"`
	Kernel          string            `json:"kernel"`
	Arch            string            `json:"arch"`
	CPUs            int               `json:"cpus"`
	CPUModel        string            `json:"cpu_model"`
	CPUPercent      *float64          `json:"cpu_percent"`
	Uptime          uint64            `json:"uptime_seconds"`
	MemoryTotal     *uint64           `json:"memory_total"`
	MemoryAvailable *uint64           `json:"memory_available"`
	DiskTotal       *uint64           `json:"disk_total"`
	DiskFree        *uint64           `json:"disk_free"`
	BytesSent       *uint64           `json:"bytes_sent"`
	BytesRecv       *uint64           `json:"bytes_recv"`
	Errors          map[string]string `json:"errors,omitempty"`
	CollectedAt     time.Time         `json:"collected_at"`
}

type Message struct {
	CWD            string `json:"cwd,omitempty"`
	Purpose        string `json:"purpose,omitempty"`
	Ticket         string `json:"ticket,omitempty"`
	Target         string `json:"target,omitempty"`
	Cols           int    `json:"cols,omitempty"`
	Rows           int    `json:"rows,omitempty"`
	Type           string `json:"type"`
	Version        int    `json:"version,omitempty"`
	ID             string `json:"id,omitempty"`
	Name           string `json:"name,omitempty"`
	Info           *Info  `json:"info,omitempty"`
	Command        string `json:"command,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	Stream         string `json:"stream,omitempty"`
	Data           []byte `json:"data,omitempty"`
	ExitCode       *int   `json:"exit_code,omitempty"`
	Error          string `json:"error,omitempty"`
	TimedOut       bool   `json:"timed_out,omitempty"`
	Truncated      bool   `json:"truncated,omitempty"`
}

type Node struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	JoinMode     string     `json:"join_mode"`
	Admission    string     `json:"admission"`
	Status       string     `json:"status"`
	Online       bool       `json:"online"`
	KeyExpiresAt *time.Time `json:"key_expires_at,omitempty"`
	JoinedAt     *time.Time `json:"joined_at,omitempty"`
	Info         Info       `json:"info"`
	CreatedAt    time.Time  `json:"created_at"`
	LastSeen     time.Time  `json:"last_seen"`
}

func (n *Node) SetStatus(connected bool) {
	n.Online = n.Admission == "approved" && connected
	switch n.Admission {
	case "approved":
		n.Status = "offline"
		if n.Online {
			n.Status = "online"
		}
	case "invited":
		n.Status = "pending"
	case "review":
		n.Status = "review"
	case "rejected":
		n.Status = "rejected"
	case "expired":
		n.Status = "expired"
	case "deleted":
		n.Status = "deleted"
	}
}

type Command struct {
	ID             string     `json:"id"`
	NodeID         string     `json:"node_id"`
	Command        string     `json:"command"`
	TimeoutSeconds int        `json:"timeout_seconds"`
	Status         string     `json:"status"`
	Stdout         []byte     `json:"stdout"`
	Stderr         []byte     `json:"stderr"`
	ExitCode       *int       `json:"exit_code"`
	Error          string     `json:"error"`
	Truncated      bool       `json:"truncated"`
	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at"`
}

func RandomID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func Endpoint(raw string, websocket bool) (string, error) {
	if !strings.Contains(raw, "://") {
		return "", fmt.Errorf("server URL must include a protocol prefix")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("invalid server URL")
	}
	switch u.Scheme {
	case "https", "wss":
		if websocket {
			u.Scheme = "wss"
		} else {
			u.Scheme = "https"
		}
	case "http", "ws":
		if websocket {
			u.Scheme = "ws"
		} else {
			u.Scheme = "http"
		}
	default:
		return "", fmt.Errorf("unsupported URL scheme")
	}
	if websocket {
		if u.Path == "" || u.Path == "/" {
			u.Path = "/agent"
		}
	} else {
		u.Path = strings.TrimRight(u.Path, "/")
	}
	return u.String(), nil
}

func Terminal(status string) bool {
	return status == "succeeded" || status == "failed" || status == "timed_out" || status == "unknown"
}

const LocalNodeID = "0"

func ValidNodeID(id string) bool {
	value, err := strconv.ParseInt(id, 10, 64)
	return err == nil && value > 0 && strconv.FormatInt(value, 10) == id
}
