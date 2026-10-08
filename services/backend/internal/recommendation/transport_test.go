package recommendation

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

func TestTLSConfigurationNeverDowngrades(t *testing.T) {
	for mask := 1; mask < 15; mask++ {
		c := TLSConfig{}
		fields := []*string{&c.CAFile, &c.CertFile, &c.KeyFile, &c.ServerName}
		for i, field := range fields {
			if mask&(1<<i) != 0 {
				*field = "configured"
			}
		}
		if _, err := New("127.0.0.1:1", c); err == nil {
			t.Fatalf("accepted partial TLS: %d", mask)
		}
	}
	ca := issue(t, nil, "CA", nil, true, false)
	client := issue(t, ca, "API", []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, false, false)
	valid := TLSConfig{ca.certFile, client.certFile, client.keyFile, "ranker.test"}
	if _, err := valid.credentials(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*TLSConfig){
		"missing CA":     func(c *TLSConfig) { c.CAFile += ".missing" },
		"invalid CA":     func(c *TLSConfig) { c.CAFile = client.keyFile },
		"mismatched key": func(c *TLSConfig) { c.KeyFile = ca.keyFile },
		"missing key":    func(c *TLSConfig) { c.KeyFile += ".missing" },
	} {
		t.Run(name, func(t *testing.T) {
			c := valid
			change(&c)
			if _, err := New("127.0.0.1:1", c); err == nil {
				t.Fatal("accepted invalid TLS")
			}
		})
	}
}

// This launches the actual Rust process, with independently issued client and
// server CAs. It is opt-in locally and mandatory in the CI transport proof.
func TestRustMutualTLS(t *testing.T) {
	binary := os.Getenv("JANSETU_RECOMMENDATION_TEST_BINARY")
	if binary == "" {
		t.Skip("run make recommendation-transport-proof")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	serverCA := issue(t, nil, "server CA", nil, true, false)
	clientCA := issue(t, nil, "API CA", nil, true, false)
	foreignCA := issue(t, nil, "untrusted CA", nil, true, false)
	server := issue(t, serverCA, "ranker.test", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, false, false)
	client := issue(t, clientCA, "API", []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, false, false)
	target := startRust(t, binary, clientCA.certFile, server.certFile, server.keyFile)
	valid := TLSConfig{serverCA.certFile, client.certFile, client.keyFile, "ranker.test"}
	call := func(t *testing.T, target string, c TLSConfig, wantSuccess bool) {
		t.Helper()
		ranker, err := New(target, c)
		if err != nil {
			t.Fatal(err)
		}
		defer ranker.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		result, err := ranker.Recommend(ctx, tlsRequest())
		if wantSuccess {
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Items) != 1 || result.Items[0].Id != "public-post" {
				t.Fatalf("unexpected response: %v", result)
			}
		} else if err == nil {
			t.Fatal("unauthenticated recommendation accepted")
		}
	}
	t.Run("valid identities", func(t *testing.T) { call(t, target, valid, true) })
	for name, change := range map[string]func(*TLSConfig){
		"wrong server name": func(c *TLSConfig) { c.ServerName = "other.test" },
		"untrusted server":  func(c *TLSConfig) { c.CAFile = foreignCA.certFile },
		"untrusted client": func(c *TLSConfig) {
			identity := issue(t, foreignCA, "rogue", []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, false, false)
			c.CertFile, c.KeyFile = identity.certFile, identity.keyFile
		},
		"expired client": func(c *TLSConfig) {
			identity := issue(t, clientCA, "expired", []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, false, true)
			c.CertFile, c.KeyFile = identity.certFile, identity.keyFile
		},
		"wrong client purpose": func(c *TLSConfig) {
			identity := issue(t, clientCA, "not-an-API", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, false, false)
			c.CertFile, c.KeyFile = identity.certFile, identity.keyFile
		},
	} {
		t.Run(name, func(t *testing.T) { c := valid; change(&c); call(t, target, c, false) })
	}
	t.Run("expired server", func(t *testing.T) {
		expired := issue(t, serverCA, "ranker.test", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, false, true)
		address := startRust(t, binary, clientCA.certFile, expired.certFile, expired.keyFile)
		call(t, address, valid, false)
	})
	t.Run("no client certificate", func(t *testing.T) {
		roots := x509.NewCertPool()
		roots.AddCert(serverCA.cert)
		rejectRPC(t, target, credentials.NewTLS(&tls.Config{RootCAs: roots, ServerName: "ranker.test", MinVersion: tls.VersionTLS13}))
	})
	t.Run("plaintext rejected", func(t *testing.T) { rejectRPC(t, target, insecure.NewCredentials()) })
	t.Run("TLS never retries plaintext", func(t *testing.T) { address := startRust(t, binary, "", "", ""); call(t, address, valid, false) })
	// Failed handshakes must not leave the listener unusable.
	t.Run("healthy after rejection", func(t *testing.T) { call(t, target, valid, true) })
	t.Run("single admitted worker over TLS", func(t *testing.T) {
		address := startRust(t, binary, clientCA.certFile, server.certFile, server.keyFile, "JANSETU_RECOMMENDATION_MAX_IN_FLIGHT=1")
		call(t, address, valid, true)
	})
	for _, limit := range []string{"0", "129", "invalid", ""} {
		t.Run("invalid admission limit "+limit, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary)
			cmd.Env = append(rustEnv("127.0.0.1:0", clientCA.certFile, server.certFile, server.keyFile), "JANSETU_RECOMMENDATION_MAX_IN_FLIGHT="+limit)
			output, err := cmd.CombinedOutput()
			if err == nil || ctx.Err() != nil {
				t.Fatalf("expected prompt limit rejection, got %v: %s", err, output)
			}
		})
	}
	for name, files := range map[string][3]string{
		"partial bundle":        {clientCA.certFile, "", ""},
		"invalid CA":            {client.keyFile, server.certFile, server.keyFile},
		"mismatched server key": {clientCA.certFile, server.certFile, client.keyFile},
		"missing server key":    {clientCA.certFile, server.certFile, server.keyFile + ".missing"},
	} {
		t.Run("startup "+name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary)
			cmd.Env = rustEnv("127.0.0.1:0", files[0], files[1], files[2])
			output, err := cmd.CombinedOutput()
			if err == nil || ctx.Err() != nil {
				t.Fatalf("expected prompt startup rejection, got %v: %s", err, output)
			}
		})
	}
}

func rejectRPC(t *testing.T, target string, creds credentials.TransportCredentials) {
	t.Helper()
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(creds))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := pb.NewRecommendationServiceClient(conn).Recommend(ctx, tlsRequest()); err == nil {
		t.Fatal("unauthenticated RPC accepted")
	}
}
func tlsRequest() *pb.RecommendRequest {
	return &pb.RecommendRequest{ViewerContext: "opaque-request", SnapshotId: "opaque-snapshot", Surface: "HOME", DeadlineUnixMs: time.Now().Add(5 * time.Second).UnixMilli(), Limit: 1, Candidates: []*pb.Candidate{{Id: "public-post", Revision: 1, AuthorId: "author", DedupKey: "body", Freshness: 1}}}
}

type certificate struct {
	cert              *x509.Certificate
	key               *ecdsa.PrivateKey
	certFile, keyFile string
}

func issue(t *testing.T, parent *certificate, name string, purpose []x509.ExtKeyUsage, isCA, expired bool) *certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: name}, DNSNames: []string{name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), BasicConstraintsValid: true, IsCA: isCA, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: purpose}
	if isCA {
		template.KeyUsage |= x509.KeyUsageCertSign
	}
	if expired {
		template.NotAfter = time.Now().Add(-time.Minute)
	}
	signer, issuer := key, template
	if parent != nil {
		signer, issuer = parent.key, parent.cert
	}
	der, err := x509.CreateCertificate(rand.Reader, template, issuer, &key.PublicKey, signer)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	c := &certificate{cert, key, filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")}
	for path, block := range map[string]*pem.Block{c.certFile: {Type: "CERTIFICATE", Bytes: der}, c.keyFile: {Type: "PRIVATE KEY", Bytes: privateDER}} {
		if err := os.WriteFile(path, pem.EncodeToMemory(block), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return c
}
func rustEnv(address, ca, cert, key string) []string {
	return []string{"JANSETU_ENV=test", "JANSETU_RECOMMENDATION_ADDR=" + address, "JANSETU_RECOMMENDATION_TLS_CA_FILE=" + ca, "JANSETU_RECOMMENDATION_TLS_CERT_FILE=" + cert, "JANSETU_RECOMMENDATION_TLS_KEY_FILE=" + key}
}
func startRust(t *testing.T, binary, ca, cert, key string, extraEnv ...string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "ranker.log")
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary)
	cmd.Env = append(rustEnv(address, ca, cert, key), extraEnv...)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		log.Close()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		_ = log.Close()
		if t.Failed() {
			contents, _ := os.ReadFile(logPath)
			t.Log(strings.TrimSpace(string(contents)))
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			t.Fatal("Rust process exited before listening")
		default:
		}
		conn, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			return address
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("Rust listener did not start at %s", address)
	return ""
}
