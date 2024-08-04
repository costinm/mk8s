// Copyright Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package csrctrl

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"
)

var serialNumberLimit = new(big.Int).Lsh(big.NewInt(1), 128)

// CertificateAuthority implements a certificate authority that supports policy
// based signing. It's used by the signing controller.
type CertificateAuthority struct {

	// Chain including the signing CA (as leaf), up to the roots
	// tls.crt
	Chain []byte

	// Roots contains the ca.crt
	Roots []byte
	Key []byte

	Certificate *x509.Certificate

	PrivateKey  crypto.Signer

}

func (ca *CertificateAuthority) Init() (err error) {
		var block *pem.Block
		block, _ = pem.Decode(ca.Key)

		slog.Info("Key"," block", block.Type)
		if strings.Contains(block.Type, "RSA") {
			ca.PrivateKey, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		} else { // "EC PRIVATE KEY"
			ca.PrivateKey, err = x509.ParseECPrivateKey(block.Bytes)
		}
		if err != nil {
			return err
		}



	pemCerts := ca.Chain
	for len(pemCerts) > 0 {
		var block *pem.Block
		block, pemCerts = pem.Decode(pemCerts)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			continue
		}

		certBytes := block.Bytes
		cert, err := x509.ParseCertificate(certBytes)
		if err != nil {
			continue
		}
		slog.Info("Cert", "sub", cert.Subject, "iss", cert.Issuer)
	}

	pemCerts = ca.Roots
	for len(pemCerts) > 0 {
		var block *pem.Block
		block, pemCerts = pem.Decode(pemCerts)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			continue
		}

		certBytes := block.Bytes
		cert, err := x509.ParseCertificate(certBytes)
		if err != nil {
			continue
		}
		slog.Info("Root", "sub", cert.Subject, "iss", cert.Issuer)
	}

	return
}


// Sign signs a certificate request, applying a SigningPolicy and returns a DER
// encoded x509 certificate.
func (ca *CertificateAuthority) Sign(crDER []byte) ([]byte, error) {
	now := time.Now()

	cr, err := x509.ParseCertificateRequest(crDER)
	if err != nil {
		return nil, fmt.Errorf("unable to parse certificate request: %v", err)
	}
	if err := cr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("unable to verify certificate request signature: %v", err)
	}

	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, fmt.Errorf("unable to generate a serial number for %s: %v", cr.Subject.CommonName, err)
	}

	tmpl := &x509.Certificate{
		SerialNumber:       serialNumber,
		Subject:            cr.Subject,
		DNSNames:           cr.DNSNames,
		IPAddresses:        cr.IPAddresses,
		EmailAddresses:     cr.EmailAddresses,
		URIs:               cr.URIs,
		PublicKeyAlgorithm: cr.PublicKeyAlgorithm,
		PublicKey:          cr.PublicKey,
		Extensions:         cr.Extensions,
		ExtraExtensions:    cr.ExtraExtensions,
		NotBefore:          now,
	}
	if !tmpl.NotAfter.Before(ca.Certificate.NotAfter) {
		tmpl.NotAfter = ca.Certificate.NotAfter
	}
	if !now.Before(ca.Certificate.NotAfter) {
		return nil, fmt.Errorf("refusing to sign a certificate that expired in the past")
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Certificate, cr.PublicKey, ca.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign certificate: %v", err)
	}
	return der, nil
}

// SingCSR signs the certificate and returns a full chain.
func (s *CertificateAuthority) SignCSR(x509cr *x509.CertificateRequest) ([]byte, error) {

	currCA := s

	der, err := currCA.Sign(x509cr.Raw)
	if err != nil {
		return nil, err
	}

	_, err = x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("error decoding DER certificate bytes: %s", err.Error())
	}

	pemBytes := bytes.NewBuffer([]byte{})
	err = pem.Encode(pemBytes, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err != nil {
		return nil, fmt.Errorf("error encoding certificate PEM: %s", err.Error())
	}

	//intermediateCerts, err := util.AppendRootCerts(pemBytes.Bytes(), s.caProvider.caIntermediate.CertFile)
	//if err != nil {
	//	return nil, fmt.Errorf("failed to append intermediate certificates (%v)", err)
	//}

	//if appendRootCert {
	//	rootCerts, err := util.AppendRootCerts(intermediateCerts, s.caProvider.caLoader.CertFile)
	//	if err != nil {
	//		return nil, fmt.Errorf("failed to append root certificates (%v)", err)
	//	}
	//	return rootCerts, nil
	//}
	return pemBytes.Bytes(), nil
}

