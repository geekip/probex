package protocol

import "testing"

func TestExplicitTransportAndPort(t *testing.T) {
	for _, c := range []struct {
		raw, want string
		ws        bool
	}{
		{"wss://probe.example", "wss://probe.example/agent", true},
		{"ws://probe.example", "ws://probe.example/agent", true},
		{"wss://127.0.0.1:17892", "wss://127.0.0.1:17892/agent", true},
		{"ws://probe.example/agent", "http://probe.example/agent", false},
		{"https://probe.example", "wss://probe.example/agent", true},
		{"http://probe.example:7890", "http://probe.example:7890", false},
	} {
		got, err := Endpoint(c.raw, c.ws)
		if err != nil || got != c.want {
			t.Fatalf("%s: got %q, %v; want %q", c.raw, got, err, c.want)
		}
	}
	for _, raw := range []string{"127.0.0.1:7890", "ftp://probe.example", "ws://user:secret@probe.example", "wss://probe.example?token=secret"} {
		if _, err := Endpoint(raw, true); err == nil {
			t.Fatalf("invalid URL accepted: %s", raw)
		}
	}
}

func TestCanonicalNodeIDs(t *testing.T) {
	for _, id := range []string{"1", "42", "9223372036854775807"} {
		if !ValidNodeID(id) {
			t.Fatal("valid ID rejected", id)
		}
	}
	for _, id := range []string{"", "0", "-1", "01", "+1", "1.0", "9223372036854775808"} {
		if ValidNodeID(id) {
			t.Fatal("invalid or aliased ID accepted", id)
		}
	}
}
