package server

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"probex/internal/protocol"
	"strconv"
	"strings"
)

type forwardPair struct {
	node   string
	config Forward
}

func parseForwardPairs(args []string, network string) ([]forwardPair, error) {
	if len(args) == 0 || len(args)%2 != 0 {
		return nil, fmt.Errorf("usage: probexd forward [options] LISTEN NODE:PORT [LISTEN NODE:HOST:PORT ...]")
	}
	result := make([]forwardPair, 0, len(args)/2)
	for i := 0; i < len(args); i += 2 {
		node, target, ok := strings.Cut(args[i+1], ":")
		if !ok || !validName(node) {
			return nil, fmt.Errorf("invalid node target %q", args[i+1])
		}
		host, port := "127.0.0.1", target
		if strings.Contains(target, ":") {
			var err error
			host, port, err = net.SplitHostPort(target)
			if err != nil {
				return nil, fmt.Errorf("invalid node target %q: %w", args[i+1], err)
			}
		}
		number, err := strconv.Atoi(port)
		if err != nil {
			return nil, fmt.Errorf("invalid target port %q", port)
		}
		f := Forward{Protocol: network, ListenAddr: args[i], TargetHost: host, TargetPort: number}
		if err := validateForward(&f); err != nil {
			return nil, err
		}
		result = append(result, forwardPair{node: node, config: f})
	}
	return result, nil
}
func resolveNode(nodes []protocol.Node, name string) (protocol.Node, error) {
	for _, n := range nodes {
		if n.ID == name {
			return n, nil
		}
	}
	var selected *protocol.Node
	for i := range nodes {
		if nodes[i].Name == name {
			if selected != nil {
				return protocol.Node{}, fmt.Errorf("multiple nodes named %q; use the node ID", name)
			}
			selected = &nodes[i]
		}
	}
	if selected == nil {
		return protocol.Node{}, fmt.Errorf("node %q not found", name)
	}
	return *selected, nil
}

func forwardCommand(mode string, args []string, pairs []forwardPair, filter string, request func(string, string, any, any) error) error {
	if mode == "remove" {
		var failures []error
		for _, id := range args {
			var result map[string]bool
			if err := request("DELETE", "/api/forwards/"+id, nil, &result); err != nil {
				fmt.Fprintf(os.Stderr, "forward %s: %v\n", id, err)
				failures = append(failures, err)
			} else {
				fmt.Println("removed", id)
			}
		}
		return errors.Join(failures...)
	}
	var nodes []protocol.Node
	if mode != "list" || filter != "" {
		if err := request("GET", "/api/nodes", nil, &nodes); err != nil {
			return err
		}
	}
	if mode == "list" {
		path := "/api/forwards"
		if filter != "" {
			n, err := resolveNode(nodes, filter)
			if err != nil {
				return err
			}
			path += "?node_id=" + url.QueryEscape(n.ID)
		}
		var list []Forward
		if err := request("GET", path, nil, &list); err != nil {
			return err
		}
		fmt.Println("ID\tNODE\tNODE_ID\tPROTOCOL\tLISTEN\tTARGET\tSTATUS\tERROR")
		for _, f := range list {
			fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", f.ID, f.NodeName, f.NodeID, f.Protocol, f.ListenAddr, net.JoinHostPort(f.TargetHost, strconv.Itoa(f.TargetPort)), f.Status, f.Error)
		}
		return nil
	}
	var failures []error
	for _, pair := range pairs {
		node, err := resolveNode(nodes, pair.node)
		f := pair.config
		if err == nil {
			var result Forward
			err = request("POST", "/api/forwards", map[string]any{"node_id": node.ID, "protocol": f.Protocol, "listen_addr": f.ListenAddr, "target_host": f.TargetHost, "target_port": f.TargetPort}, &result)
			if err == nil {
				fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\t%s\n", result.ID, result.NodeName, result.NodeID, result.Protocol, result.ListenAddr, net.JoinHostPort(result.TargetHost, strconv.Itoa(result.TargetPort)), result.Status)
				if result.Status == "listen_error" {
					err = fmt.Errorf("saved forward %s: %s", result.ID, result.Error)
				}
			}
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s -> %s: %v\n", f.ListenAddr, pair.node, err)
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
