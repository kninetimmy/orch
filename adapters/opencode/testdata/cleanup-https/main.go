// This disposable HTTPS Git fixture is built by the smoke harness, never Orch.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const host = "github.example.invalid"
const prefix = "/fixture/orch-smoke.git/"

func main() {
	if len(os.Args) != 3 || !filepath.IsAbs(os.Args[1]) || !filepath.IsAbs(os.Args[2]) {
		log.Fatal("fixture requires absolute Git binary and private scratch paths")
	}
	git, root := os.Args[1], os.Args[2]
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	now := time.Now()
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1), DNSNames: []string{host},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		BasicConstraintsValid: true, IsCA: true,
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, key.Public(), key)
	if err != nil {
		log.Fatal(err)
	}
	// Only the public certificate leaves this process; the key is ephemeral.
	ca := filepath.Join(root, "cleanup-ca.pem")
	file, err := os.OpenFile(ca, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		log.Fatal(err)
	}
	if err := pem.Encode(file, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		log.Fatal(err)
	}
	if err := file.Close(); err != nil {
		log.Fatal(err)
	}

	token := rand.Text()
	pack := &cgi.Handler{
		Path: git, Args: []string{"http-backend"}, Dir: root,
		Env: []string{"GIT_PROJECT_ROOT=" + filepath.ToSlash(root), "GIT_HTTP_EXPORT_ALL=1", "REMOTE_USER=orch-smoke"},
		InheritEnv: []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "TMP", "TEMP", "TMPDIR",
			"GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM"},
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != host || r.Header.Get("X-Orch-Smoke") != token {
			http.Error(w, "private fixture", http.StatusForbidden)
			return
		}
		service, ok := strings.CutPrefix(r.URL.Path, prefix)
		if !ok || (service != "info/refs" && service != "git-upload-pack" && service != "git-receive-pack") {
			http.NotFound(w, r)
			return
		}
		r.URL.Path = "/origin.git/" + service
		pack.ServeHTTP(w, r)
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	server.StartTLS()
	defer server.Close()
	// Keep the recorded hostname and default HTTPS port without changing DNS,
	// binding a privileged port or letting the tunnel forward to any other host.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != host+":443" {
			http.Error(w, "unsupported fixture route", http.StatusBadGateway)
			return
		}
		upstream, err := net.DialTimeout("tcp", server.Listener.Addr().String(), 5*time.Second)
		if err != nil {
			http.Error(w, "fixture unavailable", http.StatusBadGateway)
			return
		}
		defer upstream.Close()
		client, stream, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer client.Close()
		if _, err := stream.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil || stream.Flush() != nil {
			return
		}
		go func() {
			_, _ = io.Copy(upstream, stream)
			upstream.Close()
		}()
		_, _ = io.Copy(client, upstream)
	}))
	defer proxy.Close()
	if err := json.NewEncoder(os.Stdout).Encode(map[string]string{"proxy": proxy.URL, "header": "X-Orch-Smoke: " + token}); err != nil {
		log.Fatal(err)
	}
	// EOF also stops this fixture if the owning harness dies.
	_, _ = io.Copy(io.Discard, os.Stdin)
}
