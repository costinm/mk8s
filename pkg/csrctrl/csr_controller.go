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

// An example implementation of a CSR Controller.
package csrctrl

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"log/slog"

	certv1 "k8s.io/api/certificates/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/informers/certificates/v1"
	"k8s.io/client-go/kubernetes"
)

type K8SSigner struct {
	K8SClient *kubernetes.Clientset
	Name      string
	Signer *CertificateAuthority

	csri   v1.CertificateSigningRequestInformer
}

func (k *K8SSigner) OnAdd(obj interface{}, isInInitialList bool) {
	k.OnUpdate(nil, obj)
}

func (k *K8SSigner) OnUpdate(oldObj, newObj interface{}) {
	csr := newObj.(*certv1.CertificateSigningRequest)
	block, _ := pem.Decode(csr.Spec.Request)
	if block == nil {
		slog.Info("certificate signing request is not properly encoded")
		return
	}
	x509cr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		slog.Info("unable to parse csr: %v", err)
		return
	}
	slog.Info("Received CSR", "csr", csr, "x509cr", x509cr)

	if csr.Spec.SignerName != k.Name {
		return
	}
	if csr.Status.Certificate != nil {
		// TODO: check expiration, renew ?
		return // already signed
	}
	if !isCertificateRequestApproved(csr) {
		slog.Info("CSR is not approved, Ignoring.")
	}

	slog.Info("Signing")


		cert, err := k.Signer.SignCSR(x509cr)
		if err != nil {
			return
		}
		csr.Status.Certificate = cert

		_, err = k.K8SClient.CertificatesV1().CertificateSigningRequests().UpdateStatus(context.Background(), csr, metav1.UpdateOptions{})
		if err != nil {
			return
		}
		slog.Info("CSR has been signed")

}

func (k K8SSigner) OnDelete(obj interface{}) {
}


func NewK8SSigner(cl *kubernetes.Clientset, s string, factory informers.SharedInformerFactory, signers *CertificateAuthority) *K8SSigner {
	c := &K8SSigner{
		K8SClient: cl,
		Name: s,
		Signer:    signers,
	}
	c.csri = factory.Certificates().V1().CertificateSigningRequests()
	c.csri.Informer().AddEventHandler(c)

	return c
}

// isCertificateRequestApproved returns true if a certificate request has the
// "Approved" condition and no "Denied" conditions; false otherwise.
func isCertificateRequestApproved(csr *certv1.CertificateSigningRequest) bool {
	approved, denied := getCertApprovalCondition(&csr.Status)
	return approved && !denied
}

func getCertApprovalCondition(status *certv1.CertificateSigningRequestStatus) (approved bool, denied bool) {
	for _, c := range status.Conditions {
		if c.Type == certv1.CertificateApproved {
			approved = true
		}
		if c.Type == certv1.CertificateDenied {
			denied = true
		}
	}
	return
}
