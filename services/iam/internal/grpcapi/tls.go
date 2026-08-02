package grpcapi

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type TLSFiles struct {
	CertFile     string
	KeyFile      string
	ClientCAFile string
}

func serverCredentials(files TLSFiles) (grpc.ServerOption, error) {
	if files.CertFile == "" && files.KeyFile == "" && files.ClientCAFile == "" {
		return nil, nil
	}
	if files.CertFile == "" || files.KeyFile == "" {
		return nil, fmt.Errorf("grpc tls cert and key files are required together")
	}

	certificate, err := tls.LoadX509KeyPair(files.CertFile, files.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load grpc tls key pair: %w", err)
	}

	tlsConfig := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{certificate},
		ClientAuth:   tls.NoClientCert,
	}
	if files.ClientCAFile != "" {
		pemBytes, err := os.ReadFile(files.ClientCAFile)
		if err != nil {
			return nil, fmt.Errorf("read grpc tls client ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemBytes) {
			return nil, fmt.Errorf("parse grpc tls client ca")
		}
		tlsConfig.ClientCAs = pool
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return grpc.Creds(credentials.NewTLS(tlsConfig)), nil
}
