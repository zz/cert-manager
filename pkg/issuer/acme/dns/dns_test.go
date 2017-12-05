package dns

import (
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubeinformers "k8s.io/client-go/informers"
	kubefake "k8s.io/client-go/kubernetes/fake"

	"github.com/jetstack/cert-manager/pkg/apis/certmanager/v1alpha1"
	"github.com/jetstack/cert-manager/pkg/issuer/acme/dns/cloudflare"
)

type fixture struct {
	// Issuer resource this solver is for
	Issuer v1alpha1.GenericIssuer

	// Objects here are pre-loaded into the fake client
	KubeObjects []runtime.Object

	// Secret objects to store in the fake lister
	SecretLister []*corev1.Secret

	// the resourceNamespace to set on the solver
	ResourceNamespace string

	// certificate used in the test
	Certificate *v1alpha1.Certificate
}

func (f *fixture) solver() *Solver {
	kubeClient := kubefake.NewSimpleClientset(f.KubeObjects...)
	sharedInformerFactory := kubeinformers.NewSharedInformerFactory(kubeClient, 0)
	secretsLister := sharedInformerFactory.Core().V1().Secrets().Lister()
	for _, s := range f.SecretLister {
		sharedInformerFactory.Core().V1().Secrets().Informer().GetIndexer().Add(s)
	}
	stopCh := make(chan struct{})
	defer close(stopCh)
	sharedInformerFactory.Start(stopCh)
	return &Solver{
		issuer:            f.Issuer,
		client:            kubeClient,
		secretLister:      secretsLister,
		resourceNamespace: f.ResourceNamespace,
	}
}

func newIssuer(name, namespace string, configs []v1alpha1.ACMEIssuerDNS01Provider) *v1alpha1.Issuer {
	return &v1alpha1.Issuer{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.IssuerSpec{
			IssuerConfig: v1alpha1.IssuerConfig{
				ACME: &v1alpha1.ACMEIssuer{
					DNS01: &v1alpha1.ACMEIssuerDNS01Config{
						Providers: configs,
					},
				},
			},
		},
	}
}

func newCertificate(name, namespace, cn string, dnsNames []string, configs []v1alpha1.ACMECertificateDomainConfig) *v1alpha1.Certificate {
	return &v1alpha1.Certificate{
		Spec: v1alpha1.CertificateSpec{
			CommonName: cn,
			DNSNames:   dnsNames,
			ACME: &v1alpha1.ACMECertificateConfig{
				Config: configs,
			},
		},
	}
}

func newSecret(name, namespace string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: data,
	}
}

func TestSolverFor(t *testing.T) {
	type testT struct {
		f                  *fixture
		domain             string
		expectErr          bool
		expectedSolverType reflect.Type
	}
	tests := map[string]testT{
		"loads secret for cloudflare provider": {
			f: &fixture{
				Issuer: newIssuer("test", "default", []v1alpha1.ACMEIssuerDNS01Provider{
					{
						Name: "fake-cloudflare",
						Cloudflare: &v1alpha1.ACMEIssuerDNS01ProviderCloudflare{
							Email: "test",
							APIKey: v1alpha1.SecretKeySelector{
								LocalObjectReference: v1alpha1.LocalObjectReference{
									Name: "cloudflare-key",
								},
								Key: "api-key",
							},
						},
					},
				}),
				SecretLister: []*corev1.Secret{newSecret("cloudflare-key", "default", map[string][]byte{
					"api-key": []byte("a-cloudflare-api-key"),
				})},
				ResourceNamespace: "default",
				Certificate: newCertificate("test", "default", "example.com", nil, []v1alpha1.ACMECertificateDomainConfig{
					{
						Domains: []string{"example.com"},
						DNS01: &v1alpha1.ACMECertificateDNS01Config{
							Provider: "fake-cloudflare",
						},
					},
				}),
			},
			domain:             "example.com",
			expectedSolverType: reflect.TypeOf(&cloudflare.DNSProvider{}),
		},
		"fails to load a cloudflare provider with a missing secret": {
			f: &fixture{
				Issuer: newIssuer("test", "default", []v1alpha1.ACMEIssuerDNS01Provider{
					{
						Name: "fake-cloudflare",
						Cloudflare: &v1alpha1.ACMEIssuerDNS01ProviderCloudflare{
							Email: "test",
							APIKey: v1alpha1.SecretKeySelector{
								LocalObjectReference: v1alpha1.LocalObjectReference{
									Name: "cloudflare-key",
								},
								Key: "api-key",
							},
						},
					},
				}),
				// don't include any secrets in the lister
				SecretLister:      []*corev1.Secret{},
				ResourceNamespace: "default",
				Certificate: newCertificate("test", "default", "example.com", nil, []v1alpha1.ACMECertificateDomainConfig{
					{
						Domains: []string{"example.com"},
						DNS01: &v1alpha1.ACMECertificateDNS01Config{
							Provider: "fake-cloudflare",
						},
					},
				}),
			},
			domain:    "example.com",
			expectErr: true,
		},
		"fails to load a cloudflare provider with an invalid secret": {
			f: &fixture{
				Issuer: newIssuer("test", "default", []v1alpha1.ACMEIssuerDNS01Provider{
					{
						Name: "fake-cloudflare",
						Cloudflare: &v1alpha1.ACMEIssuerDNS01ProviderCloudflare{
							Email: "test",
							APIKey: v1alpha1.SecretKeySelector{
								LocalObjectReference: v1alpha1.LocalObjectReference{
									Name: "cloudflare-key",
								},
								Key: "api-key",
							},
						},
					},
				}),
				SecretLister: []*corev1.Secret{newSecret("cloudflare-key", "default", map[string][]byte{
					"api-key-oops": []byte("a-cloudflare-api-key"),
				})},
				ResourceNamespace: "default",
				Certificate: newCertificate("test", "default", "example.com", nil, []v1alpha1.ACMECertificateDomainConfig{
					{
						Domains: []string{"example.com"},
						DNS01: &v1alpha1.ACMECertificateDNS01Config{
							Provider: "fake-cloudflare",
						},
					},
				}),
			},
			domain:    "example.com",
			expectErr: true,
		},
		"fails to load a provider with no config set for the domain": {
			f: &fixture{
				Issuer: newIssuer("test", "default", []v1alpha1.ACMEIssuerDNS01Provider{
					{
						Name: "fake-cloudflare",
						Cloudflare: &v1alpha1.ACMEIssuerDNS01ProviderCloudflare{
							Email: "test",
							APIKey: v1alpha1.SecretKeySelector{
								LocalObjectReference: v1alpha1.LocalObjectReference{
									Name: "cloudflare-key",
								},
								Key: "api-key",
							},
						},
					},
				}),
				SecretLister: []*corev1.Secret{newSecret("cloudflare-key", "default", map[string][]byte{
					"api-key": []byte("a-cloudflare-api-key"),
				})},
				ResourceNamespace: "default",
				Certificate: newCertificate("test", "default", "example.com", nil, []v1alpha1.ACMECertificateDomainConfig{
					{
						Domains: []string{"example-oops.com"},
						DNS01: &v1alpha1.ACMECertificateDNS01Config{
							Provider: "fake-cloudflare",
						},
					},
				}),
			},
			domain:    "example.com",
			expectErr: true,
		},
		"fails to load a provider with a non-existent provider set for the domain": {
			f: &fixture{
				Issuer: newIssuer("test", "default", []v1alpha1.ACMEIssuerDNS01Provider{
					{
						Name: "fake-cloudflare",
						Cloudflare: &v1alpha1.ACMEIssuerDNS01ProviderCloudflare{
							Email: "test",
							APIKey: v1alpha1.SecretKeySelector{
								LocalObjectReference: v1alpha1.LocalObjectReference{
									Name: "cloudflare-key",
								},
								Key: "api-key",
							},
						},
					},
				}),
				SecretLister: []*corev1.Secret{newSecret("cloudflare-key", "default", map[string][]byte{
					"api-key": []byte("a-cloudflare-api-key"),
				})},
				ResourceNamespace: "default",
				Certificate: newCertificate("test", "default", "example.com", nil, []v1alpha1.ACMECertificateDomainConfig{
					{
						Domains: []string{"example.com"},
						DNS01: &v1alpha1.ACMECertificateDNS01Config{
							Provider: "fake-cloudflare-oops",
						},
					},
				}),
			},
			domain:    "example.com",
			expectErr: true,
		},
	}
	testFn := func(test testT) func(*testing.T) {
		return func(t *testing.T) {
			s := test.f.solver()
			dnsSolver, err := s.solverFor(test.f.Certificate, test.domain)
			if err != nil && !test.expectErr {
				t.Errorf("expected solverFor to not error, but got: %s", err.Error())
				return
			}
			typeOfSolver := reflect.TypeOf(dnsSolver)
			if typeOfSolver != test.expectedSolverType {
				t.Errorf("expected solver of type %q but got one of type %q", test.expectedSolverType, typeOfSolver)
				return
			}
		}
	}
	for name, test := range tests {
		t.Run(name, testFn(test))
	}
}
