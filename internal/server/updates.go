package server

import "sync"

type updateScope struct {
	Topic  string `json:"topic,omitempty"`
	NodeID string `json:"node_id,omitempty"`
}

type updateRequest struct {
	Type string `json:"type"`
	updateScope
}

type updateHub struct {
	mu     sync.Mutex
	owners map[*fileOwner]map[updateScope]bool
}

func (s *Server) validUpdateScope(scope updateScope) bool {
	switch scope.Topic {
	case "nodes":
		return scope.NodeID == ""
	case "commands", "forwards":
		return s.nodeEnabled(scope.NodeID)
	}
	return false
}

func queueUpdate(owner *fileOwner, event fileEvent) {
	select {
	case owner.notices <- event:
	default:
		owner.cancel()
	}
}

func (s *Server) publishUpdate(topic, nodeID, id string) {
	scope := updateScope{Topic: topic, NodeID: nodeID}
	if topic == "nodes" {
		scope.NodeID, id = "", nodeID
	}
	s.publishChange(scope, fileEvent{Type: "change", Topic: topic, NodeID: scope.NodeID, ID: id})
}

func (s *Server) publishChange(scope updateScope, event fileEvent) {
	hub := &s.updates
	hub.mu.Lock()
	defer hub.mu.Unlock()
	for owner, scopes := range hub.owners {
		if scopes[scope] {
			queueUpdate(owner, event)
		}
	}
}

func (s *Server) subscribeUpdate(owner *fileOwner, scope updateScope) {
	hub := &s.updates
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.owners == nil {
		hub.owners = make(map[*fileOwner]map[updateScope]bool)
	}
	scopes := hub.owners[owner]
	if scopes == nil {
		scopes = make(map[updateScope]bool)
		hub.owners[owner] = scopes
	}
	scopes[scope] = true
	queueUpdate(owner, scope.event("subscribed"))
}

func (scope updateScope) event(kind string) fileEvent {
	return fileEvent{Type: kind, Topic: scope.Topic, NodeID: scope.NodeID}
}

func (s *Server) unsubscribeUpdate(owner *fileOwner, scope updateScope) {
	hub := &s.updates
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if scope.Topic != "" {
		delete(hub.owners[owner], scope)
		if len(hub.owners[owner]) != 0 {
			return
		}
	}
	delete(hub.owners, owner)
}
