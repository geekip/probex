package server_test

import (
	"context"
	"net/http"
	"probex/internal/protocol"
	"probex/internal/server"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type updateMessage struct {
	Type   string `json:"type"`
	Topic  string `json:"topic"`
	NodeID string `json:"node_id"`
	ID     string `json:"id"`
	Error  string `json:"error"`
}

func openUpdateSession(t *testing.T, f *fixture, ctx context.Context) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(f.http.URL, "http")+"/api/file-sessions", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + f.admin}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	var session updateMessage
	if err := wsjson.Read(ctx, c, &session); err != nil || session.Type != "session" {
		t.Fatalf("session event = %+v, %v", session, err)
	}
	return c
}

func TestUpdateSubscriptions(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	read := func(c *websocket.Conn, want updateMessage) {
		t.Helper()
		var got updateMessage
		if err := wsjson.Read(ctx, c, &got); err != nil || got != want {
			t.Fatalf("got %+v, %v, want %+v", got, err, want)
		}
	}
	send := func(c *websocket.Conn, message updateMessage) {
		t.Helper()
		if err := wsjson.Write(ctx, c, message); err != nil {
			t.Fatal(err)
		}
	}
	subscribe := func(c *websocket.Conn, scope updateMessage) {
		t.Helper()
		scope.Type = "subscribe"
		send(c, scope)
		scope.Type = "subscribed"
		read(c, scope)
	}

	first, second := openUpdateSession(t, f, ctx), openUpdateSession(t, f, ctx)
	subscribe(first, updateMessage{Topic: "nodes"})
	subscribe(first, updateMessage{Topic: "commands", NodeID: node.ID})
	subscribe(first, updateMessage{Topic: "forwards", NodeID: node.ID})
	subscribe(second, updateMessage{Topic: "nodes"})

	f.request(http.MethodPatch, "/api/nodes/"+node.ID, map[string]string{"name": "renamed"}, 200, nil)
	read(first, updateMessage{Type: "change", Topic: "nodes", ID: node.ID})
	read(second, updateMessage{Type: "change", Topic: "nodes", ID: node.ID})
	send(second, updateMessage{Type: "unsubscribe", Topic: "nodes"})
	subscribe(second, updateMessage{Topic: "forwards", NodeID: node.ID})
	f.request(http.MethodPatch, "/api/nodes/"+node.ID, map[string]string{"name": "renamed-again"}, 200, nil)
	read(first, updateMessage{Type: "change", Topic: "nodes", ID: node.ID})

	command := f.command(node.ID, "echo update", 10)
	read(first, updateMessage{Type: "change", Topic: "commands", NodeID: node.ID, ID: command.ID})
	read(first, updateMessage{Type: "change", Topic: "commands", NodeID: node.ID, ID: command.ID})
	read(first, updateMessage{Type: "output", Topic: "commands", NodeID: node.ID, ID: command.ID})
	read(first, updateMessage{Type: "change", Topic: "commands", NodeID: node.ID, ID: command.ID})

	var mapping server.Forward
	body := forwardBody(node.ID, freeForwardAddress(t), "127.0.0.1:1")
	f.request(http.MethodPost, "/api/forwards", body, 201, &mapping)
	notification := updateMessage{Type: "change", Topic: "forwards", NodeID: node.ID, ID: mapping.ID}
	read(first, notification)
	read(second, notification)
	f.request(http.MethodDelete, "/api/forwards/"+mapping.ID, nil, 200, nil)
	read(first, notification)
	read(second, notification)
}

func TestLocalUpdatesAndEnrollmentExpiry(t *testing.T) {
	f := newFixtureWithConfig(t, server.Config{LocalNode: true, EnrollmentTTL: 300 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := openUpdateSession(t, f, ctx)
	roundTrip := func(want updateMessage) {
		t.Helper()
		request := want
		request.Type = "subscribe"
		if err := wsjson.Write(ctx, conn, request); err != nil {
			t.Fatal(err)
		}
		if err := wsjson.Read(ctx, conn, &request); err != nil || request != want {
			t.Fatalf("got %+v, %v, want %+v", request, err, want)
		}
	}
	roundTrip(updateMessage{Type: "subscribed", Topic: "commands", NodeID: protocol.LocalNodeID})
	roundTrip(updateMessage{Type: "subscribed", Topic: "forwards", NodeID: protocol.LocalNodeID})
	roundTrip(updateMessage{Type: "subscribed", Topic: "nodes"})
	var enrollment struct {
		Node protocol.Node
	}
	f.request(http.MethodPost, "/api/nodes", map[string]string{"name": "expiring"}, 201, &enrollment)
	for range 2 {
		var event updateMessage
		if err := wsjson.Read(ctx, conn, &event); err != nil || event != (updateMessage{Type: "change", Topic: "nodes", ID: enrollment.Node.ID}) {
			t.Fatalf("node creation or expiry notice = %+v, %v", event, err)
		}
	}
	var expired protocol.Node
	f.request(http.MethodGet, "/api/nodes/"+enrollment.Node.ID, nil, 200, &expired)
	if expired.Status != "expired" {
		t.Fatalf("status = %q, want expired", expired.Status)
	}
}
