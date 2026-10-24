package rpcsecurity

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"google.golang.org/grpc/credentials"
)

func ServerCredentials(certificateFile, keyFile, clientCAFile, clientIdentity string) (credentials.TransportCredentials, error) {
	if clientIdentity == "" {
		return nil, errors.New("expected client certificate URI SAN is required")
	}
	certificate, err := tls.LoadX509KeyPair(certificateFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load server certificate: %w", err)
	}
	clientCAs, err := loadCertPool(clientCAFile)
	if err != nil {
		return nil, fmt.Errorf("load client CA: %w", err)
	}
	return credentials.NewTLS(&tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
				return errors.New("client certificate was not verified")
			}
			for _, uri := range state.PeerCertificates[0].URIs {
				if uri.String() == clientIdentity {
					return nil
				}
			}
			return errors.New("client certificate URI SAN does not match the Control identity")
		},
	}), nil
}

func ClientCredentials(certificateFile, keyFile, serverCAFile, serverName string) (credentials.TransportCredentials, error) {
	if serverName == "" {
		return nil, errors.New("TLS server name is required")
	}
	certificate, err := tls.LoadX509KeyPair(certificateFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load client certificate: %w", err)
	}
	serverCAs, err := loadCertPool(serverCAFile)
	if err != nil {
		return nil, fmt.Errorf("load server CA: %w", err)
	}
	return credentials.NewTLS(&tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{certificate},
		RootCAs:      serverCAs,
		ServerName:   serverName,
	}), nil
}

func loadCertPool(path string) (*x509.CertPool, error) {
	if path == "" {
		return nil, errors.New("CA certificate path is required")
	}
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("CA file contains no valid certificates")
	}
	return pool, nil
}
