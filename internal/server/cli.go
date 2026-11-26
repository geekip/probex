package server

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"probex/internal/protocol"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func Main(args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return listen(args)
	}
	if args[0] == "listen" {
		return listen(args[1:])
	}
	if args[0] == "create-tls" || args[0] == "create-ca" {
		return certificateCommand(args[0], args[1:])
	}
	return manage(args[0], args[1:])
}
func listen(args []string) error {
	directory, err := programDirectory()
	if err != nil {
		return err
	}
	address := "127.0.0.1:17892"
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		address = args[0]
		args = args[1:]
	}
	flags := flag.NewFlagSet("listen", flag.ContinueOnError)
	flags.StringVar(&address, "listen", address, "listen address")
	admin := flags.String("admin-token", os.Getenv("PROBEX_ADMIN_TOKEN"), "management token")
	adminFile := flags.String("admin-token-file", "", "management token file")
	db := flags.String("db", filepath.Join(directory, "probex.db"), "database file")
	cert := flags.String("tls-cert", "", "TLS certificate file")
	key := flags.String("tls-key", "", "TLS key file")
	registration := flags.Bool("allow-registration", true, "allow clients to submit registration requests")
	autoApprove := flags.Bool("auto-approve", true, "automatically approve new registration requests")
	enableAgent := flags.Bool("enable-agent", true, "enable the built-in local node")
	joinTTL := flags.Duration("join-ttl", 24*time.Hour, "join key validity period")
	if err := flags.Parse(booleanArgs(args)); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	if *joinTTL <= 0 {
		return fmt.Errorf("--join-ttl must be positive")
	}
	if (*cert == "") != (*key == "") {
		return fmt.Errorf("both --tls-cert and --tls-key required")
	}
	if *adminFile != "" {
		*admin, err = savedAdminToken(*adminFile, false)
		if err != nil {
			return err
		}
	} else if *admin == "" {
		path := filepath.Join(directory, "probexd.token")
		*admin, err = savedAdminToken(path, true)
		if err != nil {
			return err
		}
		log.Printf("management token: %s", path)
	}

	store, err := OpenStore(*db)
	if err != nil {
		return err
	}
	defer store.Close()
	var publicCertificates []byte
	if *cert != "" {
		data, err := os.ReadFile(*cert)
		if err != nil {
			return err
		}
		for len(data) != 0 {
			block, rest := pem.Decode(data)
			if block == nil {
				break
			}
			data = rest
			if block.Type == "CERTIFICATE" {
				if _, err := x509.ParseCertificate(block.Bytes); err != nil {
					return err
				}
				publicCertificates = append(publicCertificates, pem.EncodeToMemory(block)...)
			}
		}
		if len(publicCertificates) == 0 {
			return fmt.Errorf("TLS file contains no certificates")
		}
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()
	app, err := New(store, Config{AdminToken: *admin, AllowRegistration: *registration, AutoApprove: *autoApprove, CA: string(publicCertificates), EnrollmentTTL: *joinTTL, LocalNode: *enableAgent})
	if err != nil {
		return err
	}
	defer app.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		return err
	}
	app.config.Port, err = strconv.Atoi(port)
	if err != nil {
		return err
	}
	httpServer := &http.Server{Addr: address, Handler: app.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		app.Close()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdown); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()
	log.Printf("probexd %s listening on %s", protocol.Version, listener.Addr())
	if *cert != "" {
		err = httpServer.ServeTLS(listener, *cert, *key)
	} else {
		err = httpServer.Serve(listener)
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
func manage(action string, args []string) error {
	if action != "cp" && action != "forward" && action != "list" && action != "add" && action != "approve" && action != "reject" && action != "remove" && action != "exec" {
		return fmt.Errorf("unknown command %q", action)
	}
	forwardMode := "create"
	if action == "forward" && len(args) > 0 && (args[0] == "list" || args[0] == "remove") {
		forwardMode, args = args[0], args[1:]
	}
	f := flag.NewFlagSet(action, flag.ContinueOnError)
	serverURL := f.String("server", "http://127.0.0.1:17892", "management server URL")
	token := f.String("admin-token", os.Getenv("PROBEX_ADMIN_TOKEN"), "management token")
	tokenFile := f.String("admin-token-file", "", "management token file")
	var name, newName, command string
	var timeout int
	if action != "cp" {
		f.StringVar(&name, "client", "", "node name or ID")
		f.StringVar(&newName, "name", "", "new node name")
		f.StringVar(&command, "command", "", "shell command")
		f.IntVar(&timeout, "timeout", 60, "remote command timeout in seconds")
	}
	ca := f.String("ca", "", "custom CA file")
	recursive, force := false, false
	if action == "cp" {
		f.Usage = func() {
			fmt.Fprintln(f.Output(), "Usage: probexd cp [-r] [-f] [--server URL] SOURCE... DESTINATION")
			fmt.Fprintln(f.Output(), "Use NAME:PATH or ID:PATH for a node; unprefixed paths belong to the CLI host.")
			fmt.Fprintln(f.Output(), "Prefix local names containing ':' with './'. Local Windows drive letters take precedence over node names.")
			f.PrintDefaults()
		}
		f.BoolVar(&recursive, "r", false, "copy directories recursively, preserving empty directories")
		f.BoolVar(&force, "f", false, "overwrite existing files without prompting")
	}
	network := "tcp"
	if action == "forward" {
		f.StringVar(&network, "protocol", "tcp", "forwarding protocol: tcp or udp")
	}
	if err := f.Parse(args); err != nil {
		return err
	}
	var paths []copyPath
	var pairs []forwardPair
	if action == "forward" {
		rest := f.Args()
		if forwardMode == "create" && len(rest) > 0 && (rest[0] == "list" || rest[0] == "remove") {
			forwardMode = rest[0]
			rest = rest[1:]
		}
		args = rest
		switch forwardMode {
		case "list":
			if len(args) != 0 {
				return fmt.Errorf("forward list takes no positional arguments")
			}
		case "remove":
			if len(args) == 0 {
				return fmt.Errorf("forward remove requires one or more IDs")
			}
			for _, id := range args {
				if !protocol.ValidNodeID(id) {
					return fmt.Errorf("invalid forward ID %q", id)
				}
			}
		default:
			var err error
			pairs, err = parseForwardPairs(args, network)
			if err != nil {
				return err
			}
		}
	} else if action == "cp" {
		var err error
		paths, err = parseCopyPaths(f.Args(), runtime.GOOS == "windows")
		if err != nil {
			return err
		}
	} else if f.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	base, err := protocol.Endpoint(*serverURL, false)
	if err != nil {
		return err
	}
	local := localManagement(base)
	if *tokenFile != "" {
		*token, err = savedAdminToken(*tokenFile, false)
		if err != nil {
			return err
		}
	} else if *token == "" && local {
		directory, e := programDirectory()
		if e != nil {
			return e
		}
		*token, err = savedAdminToken(filepath.Join(directory, "probexd.token"), false)
		if err != nil {
			return err
		}
	}
	if *token == "" {
		return fmt.Errorf("management token required: use --admin-token-file, --admin-token or PROBEX_ADMIN_TOKEN")
	}
	client, err := managementClient(base, *ca)
	if err != nil {
		return err
	}
	client.Timeout = 15 * time.Second
	explicitServer := false
	f.Visit(func(v *flag.Flag) {
		if v.Name == "server" {
			explicitServer = true
		}
	})
	if !explicitServer {
		base, err = defaultManagementServer(client, base)
		if err != nil {
			return err
		}
	}
	if action == "cp" {
		return copyCommand(client, base, *token, paths, recursive, force)
	}
	request := func(method, path string, body any, result any) error {
		var data []byte
		if body != nil {
			var err error
			data, err = json.Marshal(body)
			if err != nil {
				return err
			}
		}
		req, err := http.NewRequest(method, base+path, bytes.NewReader(data))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+*token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
			if err != nil {
				return err
			}
			return fmt.Errorf("server %d: %s", resp.StatusCode, b)
		}
		return json.NewDecoder(resp.Body).Decode(result)
	}
	if action == "forward" {
		return forwardCommand(forwardMode, args, pairs, name, request)
	}
	if action == "add" {
		var result struct {
			Key string `json:"key"`
		}
		if err := request("POST", "/api/nodes", map[string]string{"name": newName}, &result); err != nil {
			return err
		}
		fmt.Println(result.Key)
		return nil
	}
	var nodes []protocol.Node
	if err := request("GET", "/api/nodes", nil, &nodes); err != nil {
		return err
	}
	if action == "list" {
		fmt.Println("NAME\tSTATUS\tJOINED_AT\tID")
		for _, n := range nodes {
			joined := "—"
			if n.JoinedAt != nil {
				joined = n.JoinedAt.Local().Format(time.RFC3339)
			}
			fmt.Printf("%s\t%s\t%s\t%s\n", n.Name, n.Status, joined, n.ID)
		}
		return nil
	}
	selected, err := resolveNode(nodes, name)
	if err != nil {
		return err
	}
	node := &selected
	if action == "approve" || action == "reject" {
		var updated protocol.Node
		if err := request("POST", "/api/nodes/"+node.ID+"/"+action, map[string]string{}, &updated); err != nil {
			return err
		}
		fmt.Printf("%s\t%s\n", updated.Name, updated.Status)
		return nil
	}
	if action == "remove" {
		var result map[string]bool
		if err := request("DELETE", "/api/nodes/"+node.ID, nil, &result); err != nil {
			return err
		}
		fmt.Println("removed", node.Name)
		return nil
	}
	var c protocol.Command
	if err := request("POST", "/api/nodes/"+node.ID+"/commands", map[string]any{"command": command, "timeout_seconds": timeout}, &c); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "command", c.ID)
	for !protocol.Terminal(c.Status) {
		time.Sleep(500 * time.Millisecond)
		if err := request("GET", "/api/commands/"+c.ID, nil, &c); err != nil {
			return err
		}
	}
	if _, err := os.Stdout.Write(c.Stdout); err != nil {
		return err
	}
	if _, err := os.Stderr.Write(c.Stderr); err != nil {
		return err
	}
	if c.Truncated {
		fmt.Fprintln(os.Stderr, "[output truncated]")
	}
	if c.Error != "" {
		fmt.Fprintln(os.Stderr, c.Error)
	}
	if c.Status != "succeeded" {
		code := 1
		if c.ExitCode != nil && *c.ExitCode > 0 && *c.ExitCode <= 255 {
			code = *c.ExitCode
		}
		return &protocol.ExitError{Code: code}
	}
	return nil
}
