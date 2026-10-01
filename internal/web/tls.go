package web

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/libdns/cloudflare"
	"github.com/libdns/desec"
	"github.com/libdns/digitalocean"
	"github.com/libdns/hetzner"
	"github.com/libdns/rfc2136"
	"github.com/libdns/route53"
	"github.com/mholt/acmez/v3/acme"
	"go.uber.org/zap"

	"github.com/anand34577/gorget/internal/config"
)

// TLSManager provides certificates for every TLS mode.
type TLSManager struct {
	cfg     config.Config
	log     *slog.Logger
	magic   *certmagic.Config
	acme    *certmagic.ACMEIssuer
	mu      sync.RWMutex
	custom  *tls.Certificate
	modTime time.Time
	lastErr string
	caCert  *x509.Certificate
	caKey   *ecdsa.PrivateKey
	caPEM   []byte
	storage certmagic.Storage
}

// TLSStatus is shown in the admin console.
type TLSStatus struct {
	Mode      string    `json:"mode"`
	Domains   []string  `json:"domains"`
	Issuer    string    `json:"issuer"`
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`
	Error     string    `json:"error,omitempty"`
	Staging   bool      `json:"staging"`
}

var dnsProviders = map[string]func() certmagic.DNSProvider{
	"cloudflare":   func() certmagic.DNSProvider { return &cloudflare.Provider{} },
	"digitalocean": func() certmagic.DNSProvider { return &digitalocean.Provider{} },
	"hetzner":      func() certmagic.DNSProvider { return &hetzner.Provider{} },
	"desec":        func() certmagic.DNSProvider { return &desec.Provider{} },
	"rfc2136":      func() certmagic.DNSProvider { return &rfc2136.Provider{} },
	"route53":      func() certmagic.DNSProvider { return &route53.Provider{} },
}

// NewTLSManager creates the certificate source for cfg.TLS.Mode. storage (optional) replaces the
// local certificate directory, e.g. with the shared database when several instances run.
func NewTLSManager(cfg config.Config, log *slog.Logger, storage certmagic.Storage) (*TLSManager, error) {
	m := &TLSManager{cfg: cfg, log: log, storage: storage}
	switch cfg.TLS.Mode {
	case config.TLSModeACME, config.TLSModeACMEDNS:
		return m, m.initACME()
	case config.TLSModeCustom:
		return m, m.loadCustom()
	case config.TLSModeInternalCA:
		return m, m.initInternalCA()
	}
	return m, nil
}

func (m *TLSManager) initACME() error {
	tc := m.cfg.TLS
	var storage certmagic.Storage = &certmagic.FileStorage{Path: filepath.Join(m.cfg.DataDir, "certs")}
	if m.storage != nil {
		storage = m.storage
	}
	zl, _ := zap.NewProduction()
	cache := certmagic.NewCache(certmagic.CacheOptions{
		GetConfigForCert: func(certmagic.Certificate) (*certmagic.Config, error) { return m.magic, nil },
		Logger:           zl,
	})
	m.magic = certmagic.New(cache, certmagic.Config{
		Storage: storage,
		Logger:  zl,
		OnEvent: func(ctx context.Context, event string, data map[string]any) error {
			switch event {
			case "cert_obtained", "cert_renewed":
				m.setErr("")
				m.log.Info("TLS certificate "+event[5:], "identifier", data["identifier"], "issuer", data["issuer"])
			case "cert_failed":
				msg := fmt.Sprint(data["error"])
				m.setErr(msg)
				m.log.Error("TLS certificate issuance failed", "identifier", data["identifier"], "renewal", data["renewal"], "err", msg)
			}
			return nil
		},
	})
	ca := certmagic.LetsEncryptProductionCA
	if tc.Staging {
		ca = certmagic.LetsEncryptStagingCA
	}
	if tc.ACMEDirectory != "" {
		ca = tc.ACMEDirectory
	}
	tmpl := certmagic.ACMEIssuer{
		CA:     ca,
		Email:  tc.Email,
		Agreed: true,
		Logger: zl,
	}
	if tc.EABKeyID != "" {
		tmpl.ExternalAccount = &acme.EAB{KeyID: tc.EABKeyID, MACKey: tc.EABMACKey}
	}
	if m.cfg.HTTP.RedirectListen == "" {
		tmpl.DisableHTTPChallenge = true
	}
	var dnsMgr *certmagic.DNSManager
	if tc.Mode == config.TLSModeACMEDNS {
		newProv, ok := dnsProviders[tc.DNSProvider]
		if !ok {
			return fmt.Errorf("unknown DNS provider %q (supported: cloudflare, digitalocean, hetzner, desec, rfc2136, route53)", tc.DNSProvider)
		}
		prov := newProv()
		b, _ := json.Marshal(tc.DNSConfig)
		if err := json.Unmarshal(b, prov); err != nil {
			return fmt.Errorf("tls.dns_config: %w", err)
		}
		solver := &certmagic.DNS01Solver{DNSManager: certmagic.DNSManager{DNSProvider: prov}}
		dnsMgr = &solver.DNSManager
		tmpl.DNS01Solver = solver
		tmpl.DisableHTTPChallenge = true
		tmpl.DisableTLSALPNChallenge = true
	}
	m.acme = certmagic.NewACMEIssuer(m.magic, tmpl)
	issuers := []certmagic.Issuer{m.acme}
	if tc.FallbackCA == "zerossl" && tc.ACMEDirectory == "" && !tc.Staging {
		if tc.ZeroSSLAPIKey != "" {
			z := &certmagic.ZeroSSLIssuer{APIKey: tc.ZeroSSLAPIKey, Storage: storage, Logger: zl}
			z.CNAMEValidation = dnsMgr
			issuers = append(issuers, z)
		} else {
			m.log.Info("ZeroSSL fallback is disabled: set tls.zerossl_api_key (free account) to enable it")
		}
	}
	m.magic.Issuers = issuers
	return nil
}

// Start obtains/renews certificates in the background (ACME modes).
func (m *TLSManager) Start(ctx context.Context) {
	if m.magic == nil {
		if m.cfg.TLS.Mode == config.TLSModeInternalCA {
			go m.internalRenewLoop(ctx)
		}
		return
	}
	go func() {
		if err := m.magic.ManageAsync(ctx, m.cfg.TLS.Domains); err != nil {
			m.setErr(err.Error())
			m.log.Error("certificate management failed", "err", err)
		}
	}()
}

func (m *TLSManager) setErr(s string) {
	m.mu.Lock()
	m.lastErr = s
	m.mu.Unlock()
}

// TLSConfig returns the server TLS configuration (nil when TLS is off).
func (m *TLSManager) TLSConfig() *tls.Config {
	base := &tls.Config{
		MinVersion:       tls.VersionTLS12,
		CurvePreferences: []tls.CurveID{tls.X25519MLKEM768, tls.X25519, tls.CurveP256},
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384, tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256, tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
		},
		NextProtos: []string{"h2", "http/1.1"},
	}
	switch m.cfg.TLS.Mode {
	case config.TLSModeACME, config.TLSModeACMEDNS:
		mc := m.magic.TLSConfig()
		base.GetCertificate = mc.GetCertificate
		base.NextProtos = append(base.NextProtos, "acme-tls/1")
	case config.TLSModeCustom:
		base.GetCertificate = m.getCustom
	case config.TLSModeInternalCA:
		base.GetCertificate = m.getInternal
	default:
		return nil
	}
	return base
}

// HTTPChallengeHandler wraps h to answer ACME HTTP-01 challenges.
func (m *TLSManager) HTTPChallengeHandler(h http.Handler) http.Handler {
	if m.acme == nil {
		return h
	}
	return m.acme.HTTPChallengeHandler(h)
}

func (m *TLSManager) Status() TLSStatus {
	m.mu.RLock()
	st := TLSStatus{Mode: m.cfg.TLS.Mode, Domains: m.cfg.TLS.Domains, Error: m.lastErr, Staging: m.cfg.TLS.Staging}
	m.mu.RUnlock()
	var leaf *x509.Certificate
	switch m.cfg.TLS.Mode {
	case config.TLSModeACME, config.TLSModeACMEDNS:
		if len(m.cfg.TLS.Domains) > 0 {
			if c, err := m.magic.CacheManagedCertificate(context.Background(), m.cfg.TLS.Domains[0]); err == nil {
				leaf = c.Leaf
				if len(c.Certificate.Certificate) > 0 && leaf == nil {
					leaf, _ = x509.ParseCertificate(c.Certificate.Certificate[0])
				}
			}
		}
	case config.TLSModeCustom, config.TLSModeInternalCA:
		m.mu.RLock()
		if m.custom != nil {
			leaf = m.custom.Leaf
		}
		m.mu.RUnlock()
	}
	if leaf != nil {
		st.Issuer = leaf.Issuer.CommonName
		if len(leaf.Issuer.Organization) > 0 {
			st.Issuer = leaf.Issuer.Organization[0] + " " + st.Issuer
		}
		st.NotBefore, st.NotAfter = leaf.NotBefore, leaf.NotAfter
	}
	return st
}

// ---------- custom certificate (hot reload) ----------

func (m *TLSManager) loadCustom() error {
	st, err := os.Stat(m.cfg.TLS.CertFile)
	if err != nil {
		return err
	}
	cert, err := tls.LoadX509KeyPair(m.cfg.TLS.CertFile, m.cfg.TLS.KeyFile)
	if err != nil {
		return fmt.Errorf("load certificate: %w", err)
	}
	if cert.Leaf == nil && len(cert.Certificate) > 0 {
		cert.Leaf, _ = x509.ParseCertificate(cert.Certificate[0])
	}
	m.mu.Lock()
	m.custom, m.modTime = &cert, st.ModTime()
	m.mu.Unlock()
	return nil
}

func (m *TLSManager) getCustom(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	if st, err := os.Stat(m.cfg.TLS.CertFile); err == nil {
		m.mu.RLock()
		changed := st.ModTime().After(m.modTime)
		m.mu.RUnlock()
		if changed {
			if err := m.loadCustom(); err != nil {
				m.log.Warn("reloading certificate failed; keeping the previous one", "err", err)
				m.setErr(err.Error())
			} else {
				m.log.Info("TLS certificate reloaded", "file", m.cfg.TLS.CertFile)
			}
		}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.custom, nil
}

// ---------- internal CA ----------

func (m *TLSManager) initInternalCA() error {
	dir := filepath.Join(m.cfg.DataDir, "ca")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	certPath, keyPath := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
	if _, err := os.Stat(certPath); errors.Is(err, os.ErrNotExist) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return err
		}
		tmpl := &x509.Certificate{
			SerialNumber:          randSerial(),
			Subject:               pkix.Name{CommonName: "Gorget Internal CA", Organization: []string{"Gorget"}},
			NotBefore:             time.Now().Add(-time.Hour),
			NotAfter:              time.Now().AddDate(10, 0, 0),
			IsCA:                  true,
			KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
			BasicConstraintsValid: true,
			MaxPathLenZero:        true,
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
		if err != nil {
			return err
		}
		kb, _ := x509.MarshalECPrivateKey(key)
		if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
			return err
		}
		if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
			return err
		}
		m.log.Info("created internal certificate authority", "cert", certPath)
	}
	cp, err := os.ReadFile(certPath)
	if err != nil {
		return err
	}
	kp, err := os.ReadFile(keyPath)
	if err != nil {
		return err
	}
	cb, _ := pem.Decode(cp)
	kbk, _ := pem.Decode(kp)
	if cb == nil || kbk == nil {
		return errors.New("invalid internal CA files")
	}
	if m.caCert, err = x509.ParseCertificate(cb.Bytes); err != nil {
		return err
	}
	if m.caKey, err = x509.ParseECPrivateKey(kbk.Bytes); err != nil {
		return err
	}
	m.caPEM = cp
	return m.issueInternal()
}

// CACertPEM returns the internal CA certificate (nil in other modes).
func (m *TLSManager) CACertPEM() []byte { return m.caPEM }

func (m *TLSManager) issueInternal() error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber: randSerial(),
		Subject:      pkix.Name{CommonName: m.cfg.PublicHost()},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(0, 0, 90),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, d := range append([]string{m.cfg.PublicHost()}, m.cfg.TLS.Domains...) {
		if ip := net.ParseIP(d); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else if d != "" {
			tmpl.DNSNames = append(tmpl.DNSNames, d)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, m.caCert, &key.PublicKey, m.caKey)
	if err != nil {
		return err
	}
	leaf, _ := x509.ParseCertificate(der)
	cert := &tls.Certificate{Certificate: [][]byte{der, m.caCert.Raw}, PrivateKey: key, Leaf: leaf}
	m.mu.Lock()
	m.custom = cert
	m.mu.Unlock()
	return nil
}

func (m *TLSManager) getInternal(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.custom, nil
}

func (m *TLSManager) internalRenewLoop(ctx context.Context) {
	t := time.NewTicker(12 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.mu.RLock()
			expiring := m.custom != nil && time.Until(m.custom.Leaf.NotAfter) < 30*24*time.Hour
			m.mu.RUnlock()
			if expiring {
				if err := m.issueInternal(); err != nil {
					m.setErr(err.Error())
				}
			}
		}
	}
}

func randSerial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}
