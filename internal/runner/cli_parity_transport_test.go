package runner

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCLITLSInsecureParity(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: skipping bru parity")
	}
	if _, err := exec.LookPath("bru"); err != nil {
		t.Skip("bru CLI not installed")
	}

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tmp, cleanup := tempCollection(t)
	defer cleanup()

	writeSimpleCollection(t, tmp.root, srv.URL, false)

	gruBin := buildGruBinary(t, tmp.root)
	envPath := filepath.Join(tmp.root, "environments", "local.bru")

	bruJSON := filepath.Join(tmp.root, "bru.tls.insecure.json")
	gruJSON := filepath.Join(tmp.root, "gru.tls.insecure.json")

	bruCmd := exec.Command("bru", "run", "cases", "-r", "--env-file", envPath, "--reporter-json", bruJSON, "--insecure")
	bruCmd.Dir = tmp.root
	_ = bruCmd.Run() // allow non-zero, report still produced

	gruCmd := exec.Command(gruBin, "run", filepath.Join(tmp.root, "cases"), "-r", "--env", envPath, "--reporter-json", gruJSON, "--insecure")
	gruCmd.Dir = tmp.root
	_ = gruCmd.Run()

	bruStatuses, _, _ := parseBruStatuses(t, bruJSON)
	gruStatuses, _, _ := parseGruStatuses(t, gruJSON)
	if !equalStatusMaps(bruStatuses, gruStatuses) {
		t.Fatalf("tls insecure status mismatch bru=%v gru=%v", bruStatuses, gruStatuses)
	}
}

func TestCLITLSCACertParity(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: skipping bru parity")
	}
	if _, err := exec.LookPath("bru"); err != nil {
		t.Skip("bru CLI not installed")
	}

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// write server cert to file
	certOut, err := os.CreateTemp("", "cacert-*.pem")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(certOut.Name())
	cert := srv.TLS.Certificates[0].Certificate[0]
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: cert})
	certOut.Close()

	tmp, cleanup := tempCollection(t)
	defer cleanup()
	writeSimpleCollection(t, tmp.root, srv.URL, false)
	envPath := filepath.Join(tmp.root, "environments", "local.bru")
	gruBin := buildGruBinary(t, tmp.root)

	bruJSON := filepath.Join(tmp.root, "bru.tls.cacert.json")
	gruJSON := filepath.Join(tmp.root, "gru.tls.cacert.json")

	bruCmd := exec.Command("bru", "run", "cases", "-r", "--env-file", envPath, "--reporter-json", bruJSON, "--cacert", certOut.Name())
	bruCmd.Dir = tmp.root
	_ = bruCmd.Run()

	gruCmd := exec.Command(gruBin, "run", filepath.Join(tmp.root, "cases"), "-r", "--env", envPath, "--reporter-json", gruJSON, "--cacert", certOut.Name())
	gruCmd.Dir = tmp.root
	_ = gruCmd.Run()

	bruStatuses, _, _ := parseBruStatuses(t, bruJSON)
	gruStatuses, _, _ := parseGruStatuses(t, gruJSON)
	if !equalStatusMaps(bruStatuses, gruStatuses) {
		t.Fatalf("tls cacert status mismatch bru=%v gru=%v", bruStatuses, gruStatuses)
	}
}

func TestCLIClientCertConfigParity(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: skipping bru parity")
	}
	if _, err := exec.LookPath("bru"); err != nil {
		t.Skip("bru CLI not installed")
	}

	tmp, cleanup := tempCollection(t)
	defer cleanup()

	caCert, serverCert, serverKey, clientCert, clientKey := generateMTLS(t, tmp.root)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	ts := httptest.NewUnstartedServer(handler)
	ts.TLS = &tls.Config{
		Certificates: []tls.Certificate{mustLoadCert(t, serverCert, serverKey)},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    mustCertPool(t, caCert),
	}
	ts.StartTLS()
	defer ts.Close()

	writeSimpleCollection(t, tmp.root, ts.URL, false)
	envPath := filepath.Join(tmp.root, "environments", "local.bru")
	gruBin := buildGruBinary(t, tmp.root)

	clientCfg := filepath.Join(tmp.root, "client.json")
	os.WriteFile(clientCfg, []byte(`{"enabled":true,"certs":[{"type":"cert","domain":"*","certFilePath":"`+clientCert+`","keyFilePath":"`+clientKey+`"}]}`), 0o644)

	bruJSON := filepath.Join(tmp.root, "bru.mtls.json")
	gruJSON := filepath.Join(tmp.root, "gru.mtls.json")

	bruCmd := exec.Command("bru", "run", "cases", "-r", "--env-file", envPath, "--reporter-json", bruJSON, "--cacert", caCert, "--client-cert-config", clientCfg)
	bruCmd.Dir = tmp.root
	_ = bruCmd.Run()

	gruCmd := exec.Command(gruBin, "run", filepath.Join(tmp.root, "cases"), "-r", "--env", envPath, "--reporter-json", gruJSON, "--cacert", caCert, "--client-cert-config", clientCfg)
	gruCmd.Dir = tmp.root
	_ = gruCmd.Run()

	bruStatuses, _, _ := parseBruStatuses(t, bruJSON)
	gruStatuses, _, _ := parseGruStatuses(t, gruJSON)
	if !equalStatusMaps(bruStatuses, gruStatuses) {
		t.Fatalf("mtls status mismatch bru=%v gru=%v", bruStatuses, gruStatuses)
	}
}

func TestCLIProxyAndCookiesParity(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: skipping bru parity")
	}
	if _, err := exec.LookPath("bru"); err != nil {
		t.Skip("bru CLI not installed")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/set" {
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "abc"})
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/check" {
			if c := r.Header.Get("Cookie"); c != "sid=abc" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tmp, cleanup := tempCollection(t)
	defer cleanup()
	writeCookieCollection(t, tmp.root, srv.URL)
	envPath := filepath.Join(tmp.root, "environments", "local.bru")
	gruBin := buildGruBinary(t, tmp.root)

	bruJSON := filepath.Join(tmp.root, "bru.disablecookies.json")
	gruJSON := filepath.Join(tmp.root, "gru.disablecookies.json")
	bruCmd := exec.Command("bru", "run", "cases", "-r", "--env-file", envPath, "--reporter-json", bruJSON, "--disable-cookies")
	bruCmd.Dir = tmp.root
	_ = bruCmd.Run()
	gruCmd := exec.Command(gruBin, "run", filepath.Join(tmp.root, "cases"), "-r", "--env", envPath, "--reporter-json", gruJSON, "--disable-cookies")
	gruCmd.Dir = tmp.root
	_ = gruCmd.Run()
	bruStatuses, _, _ := parseBruStatuses(t, bruJSON)
	gruStatuses, _, _ := parseGruStatuses(t, gruJSON)
	if !equalStatusMaps(failOnly(bruStatuses), failOnly(gruStatuses)) {
		t.Fatalf("disable-cookies status mismatch bru=%v gru=%v", bruStatuses, gruStatuses)
	}
}

// Helpers

type tempColl struct{ root string }

func tempCollection(t *testing.T) (tempColl, func()) {
	root, err := os.MkdirTemp("", "gru-transport-")
	if err != nil {
		t.Fatal(err)
	}
	cleanup := func() { os.RemoveAll(root) }
	return tempColl{root: root}, cleanup
}

func writeSimpleCollection(t *testing.T, root, baseURL string, includeCookie bool) {
	envDir := filepath.Join(root, "environments")
	casesDir := filepath.Join(root, "cases")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(casesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	env := []byte("vars {\n  baseUrl: " + baseURL + "\n}\n")
	os.WriteFile(filepath.Join(envDir, "local.bru"), env, 0o644)
	os.WriteFile(filepath.Join(root, "bruno.json"), []byte(`{"name":"transport","version":"1.0","type":"collection"}`), 0o644)

	body := `meta {
  name: TLS
  seq: 1
}

get {
  url: {{baseUrl}}/ok
  auth: none
}

tests {
  test("status", function() { expect(res.status).to.equal(200); });
}
`
	if includeCookie {
		body = `meta {
  name: TLS
  seq: 1
}

get {
  url: {{baseUrl}}/set
  auth: none
}

tests {
  test("status", function() { expect(res.status).to.equal(200); });
}
`
	}
	os.WriteFile(filepath.Join(casesDir, "one.bru"), []byte(body), 0o644)
}

func writeCookieCollection(t *testing.T, root, baseURL string) {
	envDir := filepath.Join(root, "environments")
	casesDir := filepath.Join(root, "cases")
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(casesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	env := []byte("vars {\n  baseUrl: " + baseURL + "\n}\n")
	os.WriteFile(filepath.Join(envDir, "local.bru"), env, 0o644)
	os.WriteFile(filepath.Join(root, "bruno.json"), []byte(`{"name":"cookies","version":"1.0","type":"collection"}`), 0o644)

	set := `meta {
  name: SetCookie
  seq: 1
}

get {
  url: {{baseUrl}}/set
  auth: none
}

tests {
  test("status", function() { expect(res.status).to.equal(200); });
}
`
	check := `meta {
  name: CheckCookie
  seq: 2
}

get {
  url: {{baseUrl}}/check
  auth: none
}

tests {
  test("needs cookie", function() { expect(res.status).to.equal(200); });
}
`
	os.WriteFile(filepath.Join(casesDir, "one.bru"), []byte(set), 0o644)
	os.WriteFile(filepath.Join(casesDir, "two.bru"), []byte(check), 0o644)
}

func generateMTLS(t *testing.T, dir string) (caCertPath, serverCertPath, serverKeyPath, clientCertPath, clientKeyPath string) {
	// CA
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("ca key: %v", err)
	}
	caTmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "gru-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, &caTmpl, &caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("ca cert: %v", err)
	}
	caCertPath = filepath.Join(dir, "ca.pem")
	writeCert(t, caCertPath, caDER)

	// Server
	srvKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("srv key: %v", err)
	}
	srvTmpl := x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, &srvTmpl, &caTmpl, &srvKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("srv cert: %v", err)
	}
	serverCertPath = filepath.Join(dir, "server.pem")
	writeCert(t, serverCertPath, srvDER)
	serverKeyPath = filepath.Join(dir, "server.key")
	writeKey(t, serverKeyPath, srvKey)

	// Client
	cliKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("client key: %v", err)
	}
	cliTmpl := x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "gru-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	cliDER, err := x509.CreateCertificate(rand.Reader, &cliTmpl, &caTmpl, &cliKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("client cert: %v", err)
	}
	clientCertPath = filepath.Join(dir, "client.pem")
	writeCert(t, clientCertPath, cliDER)
	clientKeyPath = filepath.Join(dir, "client.key")
	writeKey(t, clientKeyPath, cliKey)

	return
}

func writeCert(t *testing.T, path string, der []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	defer f.Close()
	pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func writeKey(t *testing.T, path string, key *rsa.PrivateKey) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	defer f.Close()
	b := x509.MarshalPKCS1PrivateKey(key)
	pem.Encode(f, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: b})
}

func mustLoadCert(t *testing.T, certPath, keyPath string) tls.Certificate {
	t.Helper()
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatalf("load cert: %v", err)
	}
	return cert
}

func mustCertPool(t *testing.T, caPath string) *x509.CertPool {
	t.Helper()
	data, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("read ca: %v", err)
	}
	pool := x509.NewCertPool()
	if ok := pool.AppendCertsFromPEM(data); !ok {
		t.Fatalf("append ca failed")
	}
	return pool
}

func failOnly(m map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		if v == "fail" {
			out[k] = v
		}
	}
	return out
}
