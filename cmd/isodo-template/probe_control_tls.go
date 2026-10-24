package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

type probeTLSFiles struct{ CA, ServerCert, ServerKey, ClientCert, ClientKey, ClientURI string }

func newProbeTLS(dir string) (probeTLSFiles, error) {
	var files probeTLSFiles
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return files, err
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "probe-ca"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		return files, err
	}
	files.CA = filepath.Join(dir, "ca.pem")
	if err = os.WriteFile(files.CA, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		return files, err
	}
	leaf := func(name string, serial int64, client bool) (string, string, error) {
		key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			return "", "", e
		}
		cert := &x509.Certificate{SerialNumber: big.NewInt(serial), NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature}
		if client {
			cert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
			u, _ := url.Parse("spiffe://isodo/control")
			cert.URIs = []*url.URL{u}
		} else {
			cert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
			cert.DNSNames = []string{"probe-node"}
		}
		data, e := x509.CreateCertificate(rand.Reader, cert, ca, &key.PublicKey, caKey)
		if e != nil {
			return "", "", e
		}
		keyData, e := x509.MarshalPKCS8PrivateKey(key)
		if e != nil {
			return "", "", e
		}
		certPath, keyPath := filepath.Join(dir, name+".pem"), filepath.Join(dir, name+".key")
		if e = os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: data}), 0600); e != nil {
			return "", "", e
		}
		e = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyData}), 0600)
		return certPath, keyPath, e
	}
	files.ServerCert, files.ServerKey, err = leaf("server", 2, false)
	if err != nil {
		return files, err
	}
	files.ClientCert, files.ClientKey, err = leaf("client", 3, true)
	files.ClientURI = "spiffe://isodo/control"
	return files, err
}
