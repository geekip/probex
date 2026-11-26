package server_test

import (
	"bytes"
	"io"
	"net"
	"net/http/httptest"
	"path/filepath"
	"probex/internal/server"
	"strconv"
	"testing"
	"time"
)

// Both target transports use one endpoint, as configured by a TCP+UDP service.
func dualEchoTarget(t *testing.T) string {
	t.Helper()
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := tcp.Addr().String()
	udpAddr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		t.Fatal(err)
	}
	udp, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		tcp.Close()
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := tcp.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); io.Copy(conn, conn) }()
		}
	}()
	go func() {
		data := make([]byte, 65535)
		for {
			n, from, err := udp.ReadFromUDP(data)
			if err != nil {
				return
			}
			if _, err = udp.WriteToUDP(data[:n], from); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { tcp.Close(); udp.Close() })
	return address
}
func tcpEchoConnection(t *testing.T, address string) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", address, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
func tcpEchoMessage(t *testing.T, c net.Conn, message string) {
	t.Helper()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write([]byte(message)); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, len(message))
	if _, err := io.ReadFull(c, data); err != nil || string(data) != message {
		t.Fatal(string(data), err)
	}
}
func TestForwardNamedDualProtocolEdit(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	target := dualEchoTarget(t)
	address := freeForwardAddress(t)
	body := forwardBody(node.ID, address, target)
	body["protocol"] = "tcp+udp"
	body["name"] = "Dual echo service"
	var service server.Forward
	f.request("POST", "/api/forwards", body, 201, &service)
	if service.Name != "Dual echo service" || service.Protocol != "tcp+udp" || service.Status != "ready" {
		t.Fatal(service)
	}
	conn := tcpEchoConnection(t, address)
	tcpEchoMessage(t, conn, "before rename")
	u := udpClient(t, address)
	data, err := udpExchange(u, []byte("udp before rename"))
	if err != nil || !bytes.Equal(data, []byte("udp before rename")) {
		t.Fatal(string(data), err)
	}
	for _, network := range []string{"tcp", "udp", "tcp+udp"} {
		conflict := forwardBody(node.ID, address, target)
		conflict["protocol"] = network
		f.request("POST", "/api/forwards", conflict, 409, nil)
	}
	body["name"] = "Renamed dual service"
	var updated server.Forward
	f.request("PUT", "/api/forwards/"+service.ID, body, 200, &updated)
	if updated.ID != service.ID || updated.Name != "Renamed dual service" || !updated.CreatedAt.Equal(service.CreatedAt) {
		t.Fatal(updated)
	}
	tcpEchoMessage(t, conn, "after rename without disconnect")
	target2 := dualEchoTarget(t)
	address2 := freeForwardAddress(t)
	changed := forwardBody(node.ID, address2, target2)
	changed["protocol"] = "tcp+udp"
	changed["name"] = "Moved dual service"
	f.request("PUT", "/api/forwards/"+service.ID, changed, 200, &updated)
	if updated.ID != service.ID || updated.Status != "ready" {
		t.Fatal(updated)
	}
	tcpEchoMessage(t, tcpEchoConnection(t, address2), "new endpoint")
	newUDP := udpClient(t, address2)
	data, err = udpExchange(newUDP, []byte("new UDP endpoint"))
	if err != nil || string(data) != "new UDP endpoint" {
		t.Fatal(string(data), err)
	}
	probeTCP, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal("old TCP listener retained", err)
	}
	probeTCP.Close()
	oldUDPAddr, _ := net.ResolveUDPAddr("udp", address)
	probeUDP, err := net.ListenUDP("udp", oldUDPAddr)
	if err != nil {
		t.Fatal("old UDP listener retained", err)
	}
	probeUDP.Close()
	f.stopAgent()
	f.app.Close()
	f.http.Close()
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	f.store, err = server.OpenStore(filepath.Join(filepath.Dir(f.state), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	f.app, err = server.New(f.store, server.Config{AdminToken: f.admin})
	if err != nil {
		t.Fatal(err)
	}
	f.http = httptest.NewServer(f.app.Handler())
	f.startAgent()
	f.node()
	var restored []server.Forward
	f.request("GET", "/api/forwards", nil, 200, &restored)
	if len(restored) != 1 || restored[0].ID != service.ID || restored[0].Name != "Moved dual service" || restored[0].Protocol != "tcp+udp" {
		t.Fatal(restored)
	}
	tcpEchoMessage(t, tcpEchoConnection(t, address2), "TCP after restart")
	data, err = udpExchange(newUDP, []byte("UDP after restart"))
	if err != nil || string(data) != "UDP after restart" {
		t.Fatal(string(data), err)
	}
	// Removing UDP from the same service frees just that listener.
	changed["protocol"] = "tcp"
	f.request("PUT", "/api/forwards/"+service.ID, changed, 200, &updated)
	newUDPAddr, _ := net.ResolveUDPAddr("udp", address2)
	probeUDP, err = net.ListenUDP("udp", newUDPAddr)
	if err != nil {
		t.Fatal("UDP listener not released", err)
	}
	probeUDP.Close()
	tcpEchoMessage(t, tcpEchoConnection(t, address2), "tcp after protocol edit")
	// A failed edit cannot overwrite a different service's listen endpoint.
	conflict := forwardBody(node.ID, address, target)
	conflict["name"] = "Second service"
	var second server.Forward
	f.request("POST", "/api/forwards", conflict, 201, &second)
	changed["listen_addr"] = address
	f.request("PUT", "/api/forwards/"+service.ID, changed, 409, nil)
	var list []server.Forward
	f.request("GET", "/api/forwards?node_id="+node.ID, nil, 200, &list)
	for _, entry := range list {
		if entry.ID == service.ID && entry.ListenAddr != address2 {
			t.Fatal("failed edit changed configuration", entry)
		}
	}
	f.request("DELETE", "/api/forwards/"+service.ID, nil, 200, nil)
	probeTCP, err = net.Listen("tcp", address2)
	if err != nil {
		t.Fatal("deleted TCP listener retained", err)
	}
	probeTCP.Close()
	f.request("PUT", "/api/forwards/999999", body, 404, nil)
}
func TestForwardDualProtocolBindFailureReleasesOtherTransport(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	target := dualEchoTarget(t)
	occupied, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(occupied.Addr().(*net.TCPAddr).Port))
	body := forwardBody(node.ID, address, target)
	body["protocol"] = "tcp+udp"
	body["name"] = "Occupied TCP service"
	f.request("POST", "/api/forwards", body, 409, nil)
	udpAddr, _ := net.ResolveUDPAddr("udp", address)
	probe, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		t.Fatal("partially started UDP listener retained", err)
	}
	probe.Close()
	currentAddress := freeForwardAddress(t)
	current := forwardBody(node.ID, currentAddress, target)
	var service server.Forward
	f.request("POST", "/api/forwards", current, 201, &service)
	edit := "/api/forwards/" + service.ID
	active := tcpEchoConnection(t, currentAddress)
	tcpEchoMessage(t, active, "before failed edit")
	f.request("PUT", edit, body, 409, nil)
	tcpEchoMessage(t, active, "after failed edit")
	// An occupied UDP port prevents adding UDP to an existing TCP mapping.
	currentUDPAddr, _ := net.ResolveUDPAddr("udp", currentAddress)
	busyUDP, err := net.ListenUDP("udp", currentUDPAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer busyUDP.Close()
	body["listen_addr"] = currentAddress
	f.request("PUT", edit, body, 409, nil)
	var list []server.Forward
	f.request("GET", "/api/forwards", nil, 200, &list)
	if len(list) != 1 || list[0] != service {
		t.Fatal("failed save changed mappings", list)
	}
	tcpEchoMessage(t, active, "after failed protocol change")
	busyUDP.Close()
	f.request("PUT", edit, body, 200, &service)
	tcpEchoMessage(t, tcpEchoConnection(t, currentAddress), "TCP after enabling UDP")
	got, err := udpExchange(udpClient(t, currentAddress), []byte("UDP after enabling UDP"))
	if err != nil || string(got) != "UDP after enabling UDP" {
		t.Fatal(string(got), err)
	}
}
