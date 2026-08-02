package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
)

func main() {
	address := envOr("LISTEN_ADDRESS", "127.0.0.1:0")
	certFile := strings.TrimSpace(os.Getenv("TLS_CERT_FILE"))
	keyFile := strings.TrimSpace(os.Getenv("TLS_KEY_FILE"))
	clientCAFile := strings.TrimSpace(os.Getenv("TLS_CLIENT_CA_FILE"))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /echo", func(w http.ResponseWriter, r *http.Request) {
		headers := map[string]string{}
		identityHeaders := map[string]string{}
		for name, values := range r.Header {
			lower := strings.ToLower(name)
			joined := strings.Join(values, ",")
			headers[lower] = joined
			if strings.HasPrefix(lower, "x-identity-") {
				identityHeaders[lower] = joined
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"path":              r.URL.Path,
			"headers":           identityHeaders,
			"request_headers":   headers,
		})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	listener, err := net.Listen("tcp", address)
	if err != nil {
		fatalf("listen: %v", err)
	}

	server := &http.Server{Handler: mux}
	if certFile != "" || keyFile != "" {
		if certFile == "" || keyFile == "" {
			fatalf("TLS_CERT_FILE and TLS_KEY_FILE must be set together")
		}
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
		certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			fatalf("load tls key pair: %v", err)
		}
		tlsConfig.Certificates = []tls.Certificate{certificate}
		if clientCAFile != "" {
			pemBytes, err := os.ReadFile(clientCAFile)
			if err != nil {
				fatalf("read client ca: %v", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pemBytes) {
				fatalf("parse client ca")
			}
			tlsConfig.ClientCAs = pool
			tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		}
		listener = tls.NewListener(listener, tlsConfig)
		fmt.Fprintf(
			os.Stdout,
			`{"message":"protected echo listening","addr":%q,"tls":true}`+"\n",
			listener.Addr().String(),
		)
	} else {
		fmt.Fprintf(
			os.Stdout,
			`{"message":"protected echo listening","addr":%q,"tls":false}`+"\n",
			listener.Addr().String(),
		)
	}

	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		fatalf("serve: %v", err)
	}
}

func envOr(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
