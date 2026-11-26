package server

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func certificateCommand(action string, args []string) error {
	directory, err := programDirectory()
	if err != nil {
		return err
	}
	f := flag.NewFlagSet(action, flag.ContinueOnError)
	cert := f.String("tls-cert", filepath.Join(directory, "probexd.cert"), "server certificate chain file")
	key := f.String("tls-key", filepath.Join(directory, "probexd.key"), "server private key file")
	ca := f.String("ca", filepath.Join(directory, "probex.crt"), "CA certificate output file")
	var hosts *string
	if action == "create-tls" {
		hosts = f.String("hosts", "localhost,127.0.0.1,::1", "comma-separated certificate DNS names and IP addresses")
	}
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	if action == "create-ca" {
		pair, err := tls.LoadX509KeyPair(*cert, *key)
		if err != nil {
			return fmt.Errorf("read certificate and private key: %w", err)
		}
		data, err := exportCA(pair.Certificate)
		if err != nil {
			return err
		}
		if err := writeNewFile(*ca, data, 0644); err != nil {
			return err
		}
		fmt.Println("CA:", *ca)
		return nil
	}
	chain, privateKey, root, err := generateTLS(*hosts)
	if err != nil {
		return err
	}
	files := []struct {
		path string
		data []byte
		mode os.FileMode
	}{{*cert, chain, 0644}, {*key, privateKey, 0600}, {*ca, root, 0644}}
	// Check every output before creating any file; existing certificates are never replaced.
	paths := map[string]bool{}
	for _, file := range files {
		path, err := filepath.Abs(file.path)
		if err != nil {
			return err
		}
		if paths[path] {
			return fmt.Errorf("certificate output paths must be different")
		}
		paths[path] = true
		if _, err := os.Lstat(file.path); !os.IsNotExist(err) {
			if err != nil {
				return err
			}
			return fmt.Errorf("output already exists: %s", file.path)
		}
	}
	for _, file := range files {
		if err := writeNewFile(file.path, file.data, file.mode); err != nil {
			return err
		}
	}
	fmt.Printf("certificate: %s\nprivate key: %s\nCA: %s\nSANs: %s\n", *cert, *key, *ca, *hosts)
	return nil
}

func writeNewFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return closeErr
}

func generateTLS(hosts string) (chain, key, ca []byte, err error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	caSerial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, nil, nil, err
	}
	serverSerial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, nil, nil, err
	}
	now := time.Now()
	root := &x509.Certificate{
		SerialNumber: caSerial, Subject: pkix.Name{CommonName: "Probex CA"},
		NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(10, 0, 0),
		IsCA: true, BasicConstraintsValid: true, MaxPathLenZero: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	leaf := &x509.Certificate{
		SerialNumber: serverSerial, Subject: pkix.Name{CommonName: "Probex server"},
		NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(1, 0, 0),
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, host := range strings.Split(hosts, ",") {
		host = strings.TrimSpace(host)
		if host == "" || strings.ContainsAny(host, "/: \t\n") && net.ParseIP(host) == nil {
			return nil, nil, nil, fmt.Errorf("invalid certificate host %q; supply DNS names or IP addresses without a scheme or port", host)
		}
		if ip := net.ParseIP(host); ip != nil {
			leaf.IPAddresses = append(leaf.IPAddresses, ip)
		} else {
			leaf.DNSNames = append(leaf.DNSNames, host)
		}
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, root, root, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, nil, err
	}
	root, err = x509.ParseCertificate(rootDER)
	if err != nil {
		return nil, nil, nil, err
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, root, &serverKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(serverKey)
	if err != nil {
		return nil, nil, nil, err
	}
	ca = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})
	chain = append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}), ca...)
	return chain, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), ca, nil
}

func exportCA(chain [][]byte) ([]byte, error) {
	certs := make([]*x509.Certificate, len(chain))
	for i, data := range chain {
		cert, err := x509.ParseCertificate(data)
		if err != nil {
			return nil, err
		}
		certs[i] = cert
	}
	leaf := certs[0]
	if bytes.Equal(leaf.RawIssuer, leaf.RawSubject) && leaf.CheckSignature(leaf.SignatureAlgorithm, leaf.RawTBSCertificate, leaf.Signature) == nil {
		roots := x509.NewCertPool()
		roots.AddCert(leaf)
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots}); err != nil {
			return nil, fmt.Errorf("verify self-signed server certificate: %w", err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw}), nil
	}
	if len(certs) == 1 {
		return nil, fmt.Errorf("certificate file contains only a CA-signed leaf certificate; obtain its issuer CA chain, which cannot be recovered from the leaf certificate and private key")
	}
	root := certs[len(certs)-1]
	if !root.IsCA {
		return nil, fmt.Errorf("certificate chain does not end with a CA certificate")
	}
	roots := x509.NewCertPool()
	roots.AddCert(root)
	intermediates := x509.NewCertPool()
	for _, cert := range certs[1 : len(certs)-1] {
		intermediates.AddCert(cert)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates}); err != nil {
		return nil, fmt.Errorf("verify certificate chain: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: root.Raw}), nil
}
