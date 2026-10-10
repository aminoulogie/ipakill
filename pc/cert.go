package main

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"software.sslmate.com/src/go-pkcs12"
)

// The ipakill app runs other apps inside itself (the LiveContainer method).
// From iOS 26 on, their code must be signed on the phone with the same
// certificate as ipakill itself. plumesign only keeps the private key on disk
// (%APPDATA%\PlumeImpactor\keys\<team>\key.pem); the matching certificate is
// inside the provisioning profile the phone already has. So the phone sends
// its embedded.mobileprovision and gets back a .p12 of the two.

var (
	devCertsRe = regexp.MustCompile(`(?s)<key>DeveloperCertificates</key>\s*<array>(.*?)</array>`)
	dataRe     = regexp.MustCompile(`(?s)<data>(.*?)</data>`)
)

func plumeKeyFiles() []string {
	files, _ := filepath.Glob(filepath.Join(os.Getenv("APPDATA"), "PlumeImpactor", "keys", "*", "key.pem"))
	return files
}

// profileCerts pulls the developer certificates out of a provisioning
// profile. The profile is a signed blob with a plain XML plist inside.
func profileCerts(profile []byte) ([]*x509.Certificate, error) {
	m := devCertsRe.FindSubmatch(profile)
	if m == nil {
		return nil, fmt.Errorf("no DeveloperCertificates in the provisioning profile")
	}
	var certs []*x509.Certificate
	for _, d := range dataRe.FindAllSubmatch(m[1], -1) {
		der, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(string(d[1])), ""))
		if err != nil {
			continue
		}
		if c, err := x509.ParseCertificate(der); err == nil {
			certs = append(certs, c)
		}
	}
	return certs, nil
}

type publicKeyer interface {
	Public() crypto.PublicKey
}

func readKey(path string) (crypto.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("%s is not a PEM file", path)
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

// signingP12 returns the certificate ipakill is signed with, plus its key,
// as a password-protected .p12.
func signingP12(profile []byte) (p12 []byte, password string, err error) {
	certs, err := profileCerts(profile)
	if err != nil {
		return nil, "", err
	}
	keys := plumeKeyFiles()
	if len(keys) == 0 {
		return nil, "", fmt.Errorf("no plumesign key found - sign something once with 'ipakill' first")
	}
	for _, path := range keys {
		key, err := readKey(path)
		if err != nil {
			continue
		}
		pubDER, err := x509.MarshalPKIXPublicKey(key.(publicKeyer).Public())
		if err != nil {
			continue
		}
		for _, c := range certs {
			if !bytes.Equal(c.RawSubjectPublicKeyInfo, pubDER) {
				continue
			}
			buf := make([]byte, 12)
			rand.Read(buf)
			password = hex.EncodeToString(buf)
			// AES-256 + PBKDF2: what the OpenSSL 3 in ipakill's signer reads.
			p12, err = pkcs12.Modern2023.Encode(key, c, nil, password)
			return p12, password, err
		}
	}
	return nil, "", fmt.Errorf("none of plumesign's keys matches ipakill's certificate - re-sign ipakill from this PC")
}
