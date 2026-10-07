package recommendation

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// TLSConfig identifies the API to the ranker and pins its server trust domain.
// An empty configuration retains the isolated local/test plaintext transport.
type TLSConfig struct {
	CAFile, CertFile, KeyFile, ServerName string
}

func (c TLSConfig) Validate() error {
	if c == (TLSConfig{}) {
		return nil
	}
	if c.CAFile == "" || c.CertFile == "" || c.KeyFile == "" || c.ServerName == "" {
		return errors.New("recommendation TLS requires CA, client certificate, key and server name")
	}
	return nil
}

func (c TLSConfig) credentials() (credentials.TransportCredentials, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if c == (TLSConfig{}) {
		return insecure.NewCredentials(), nil
	}
	ca, err := os.ReadFile(c.CAFile)
	if err != nil {
		return nil, fmt.Errorf("recommendation TLS CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, errors.New("recommendation TLS CA contains no certificates")
	}
	identity, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("recommendation TLS identity: %w", err)
	}
	return credentials.NewTLS(&tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    roots, Certificates: []tls.Certificate{identity}, ServerName: c.ServerName,
	}), nil
}
