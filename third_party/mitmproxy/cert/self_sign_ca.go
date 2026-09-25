package cert

import (
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"time"

	ucert "github.com/chainreactors/utils/cert"
	"github.com/golang/groupcache/lru"
	"github.com/golang/groupcache/singleflight"
	log "github.com/sirupsen/logrus"
)

var errCaNotFound = errors.New("ca not found")

type SelfSignCA struct {
	rsa.PrivateKey
	RootCert  x509.Certificate
	StorePath string

	cache *lru.Cache
	group *singleflight.Group

	cacheMu sync.Mutex
}

func createCert() (*rsa.PrivateKey, *x509.Certificate, error) {
	subject := &pkix.Name{
		CommonName:   "mitmproxy",
		Organization: []string{"mitmproxy"},
	}
	certPEM, keyPEM, err := ucert.GenerateCACert(2048,
		ucert.WithFullSubject(*subject),
		ucert.WithValidityWindow(time.Now().Add(-48*time.Hour), 3*365*24*time.Hour),
		ucert.WithKeyUsages(x509.KeyUsageCertSign|x509.KeyUsageCRLSign),
		ucert.WithExtKeyUsages(
			x509.ExtKeyUsageServerAuth,
			x509.ExtKeyUsageClientAuth,
			x509.ExtKeyUsageEmailProtection,
			x509.ExtKeyUsageTimeStamping,
			x509.ExtKeyUsageCodeSigning,
			x509.ExtKeyUsageMicrosoftCommercialCodeSigning,
			x509.ExtKeyUsageMicrosoftServerGatedCrypto,
			x509.ExtKeyUsageNetscapeServerGatedCrypto,
		),
	)
	if err != nil {
		return nil, nil, err
	}

	cert, err := ucert.ParseCertificatePEM(certPEM)
	if err != nil {
		return nil, nil, err
	}
	key, err := ucert.ParseKeyPEM(keyPEM)
	if err != nil {
		return nil, nil, err
	}

	return key, cert, nil
}

// NewSelfSignCAMemory Create new ca only live in memory, will change when process restart
func NewSelfSignCAMemory() (CA, error) {
	key, cert, err := createCert()
	if err != nil {
		return nil, err
	}
	return &SelfSignCA{
		PrivateKey: *key,
		RootCert:   *cert,
		StorePath:  "",
		cache:      lru.New(100),
		group:      new(singleflight.Group),
	}, nil
}

// NewSelfSignCA Load ca from store path or create new ca then store
func NewSelfSignCA(path string) (CA, error) {
	storePath, err := getStorePath(path)
	if err != nil {
		return nil, err
	}

	ca := &SelfSignCA{
		StorePath: storePath,
		cache:     lru.New(100),
		group:     new(singleflight.Group),
	}

	if err := ca.load(); err != nil {
		if err != errCaNotFound {
			return nil, err
		}
	} else {
		log.Debug("load root ca")
		return ca, nil
	}

	if err := ca.create(); err != nil {
		return nil, err
	}
	log.Debug("create root ca")
	return ca, nil
}

func getStorePath(path string) (string, error) {
	if path == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(homeDir, ".mitmproxy")
	}

	if !filepath.IsAbs(path) {
		dir, err := os.Getwd()
		if err != nil {
			return "", err
		}
		path = filepath.Join(dir, path)
	}

	stat, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			err = os.MkdirAll(path, os.ModePerm)
			if err != nil {
				return "", err
			}
		} else {
			return "", err
		}
	} else {
		if !stat.Mode().IsDir() {
			return "", fmt.Errorf("path %v is not a directory, please remove this file and retry", path)
		}
	}

	return path, nil
}

func (ca *SelfSignCA) caFile() string {
	return filepath.Join(ca.StorePath, "mitmproxy-ca.pem")
}

func (ca *SelfSignCA) caCertFile() string {
	return filepath.Join(ca.StorePath, "mitmproxy-ca-cert.pem")
}

func (ca *SelfSignCA) caCertCerFile() string {
	return filepath.Join(ca.StorePath, "mitmproxy-ca-cert.cer")
}

func (ca *SelfSignCA) load() error {
	caFile := ca.caFile()
	stat, err := os.Stat(caFile)
	if err != nil {
		if os.IsNotExist(err) {
			return errCaNotFound
		}
		return err
	}

	if !stat.Mode().IsRegular() {
		return fmt.Errorf("%v is not a file", caFile)
	}

	data, err := ioutil.ReadFile(caFile)
	if err != nil {
		return err
	}

	keyDERBlock, data := pem.Decode(data)
	if keyDERBlock == nil {
		return fmt.Errorf("no PRIVATE KEY found in %v", caFile)
	}
	certDERBlock, _ := pem.Decode(data)
	if certDERBlock == nil {
		return fmt.Errorf("no CERTIFICATE found in %v", caFile)
	}

	keyPEM := pem.EncodeToMemory(keyDERBlock)
	privateKey, err := ucert.ParseKeyPEM(keyPEM)
	if err != nil {
		return err
	}
	ca.PrivateKey = *privateKey

	x509Cert, err := x509.ParseCertificate(certDERBlock.Bytes)
	if err != nil {
		return err
	}
	ca.RootCert = *x509Cert

	return nil
}

func (ca *SelfSignCA) create() error {
	key, cert, err := createCert()
	if err != nil {
		return err
	}

	ca.PrivateKey = *key
	ca.RootCert = *cert

	if err := ca.save(); err != nil {
		return err
	}
	return ca.saveCert()
}

func (ca *SelfSignCA) saveTo(out io.Writer) error {
	keyBytes, err := x509.MarshalPKCS8PrivateKey(&ca.PrivateKey)
	if err != nil {
		return err
	}
	err = pem.Encode(out, &pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes})
	if err != nil {
		return err
	}

	return pem.Encode(out, &pem.Block{Type: "CERTIFICATE", Bytes: ca.RootCert.Raw})
}

func (ca *SelfSignCA) saveCertTo(out io.Writer) error {
	return pem.Encode(out, &pem.Block{Type: "CERTIFICATE", Bytes: ca.RootCert.Raw})
}

func (ca *SelfSignCA) save() error {
	file, err := os.Create(ca.caFile())
	if err != nil {
		return err
	}
	defer file.Close()
	return ca.saveTo(file)
}

func (ca *SelfSignCA) saveCert() error {
	file, err := os.Create(ca.caCertFile())
	if err != nil {
		return err
	}
	defer file.Close()
	err = ca.saveCertTo(file)
	if err != nil {
		return err
	}

	cerFile, err := os.Create(ca.caCertCerFile())
	if err != nil {
		return err
	}
	defer cerFile.Close()
	err = ca.saveCertTo(cerFile)
	if err != nil {
		return err
	}
	return err
}

func (ca *SelfSignCA) GetRootCA() *x509.Certificate {
	return &ca.RootCert
}

func (ca *SelfSignCA) GetCert(commonName string) (*tls.Certificate, error) {
	ca.cacheMu.Lock()
	if val, ok := ca.cache.Get(commonName); ok {
		ca.cacheMu.Unlock()
		log.Debugf("ca GetCert: %v", commonName)
		return val.(*tls.Certificate), nil
	}
	ca.cacheMu.Unlock()

	val, err := ca.group.Do(commonName, func() (interface{}, error) {
		cert, err := ca.DummyCert(commonName)
		if err == nil {
			ca.cacheMu.Lock()
			ca.cache.Add(commonName, cert)
			ca.cacheMu.Unlock()
		}
		return cert, err
	})

	if err != nil {
		return nil, err
	}

	return val.(*tls.Certificate), nil
}

func (ca *SelfSignCA) DummyCert(commonName string) (*tls.Certificate, error) {
	log.Debugf("ca DummyCert: %v", commonName)

	tmpl, err := ucert.NewTemplate(
		ucert.WithFullSubject(pkix.Name{
			CommonName:   commonName,
			Organization: []string{"mitmproxy"},
		}),
		ucert.WithValidityWindow(time.Now().Add(-48*time.Hour), 365*24*time.Hour),
		ucert.WithExtKeyUsages(x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth),
		ucert.WithAutoSAN(commonName),
	)
	if err != nil {
		return nil, err
	}

	derBytes, err := ucert.SignWith(tmpl, &ca.RootCert, &ca.PrivateKey, &ca.PrivateKey)
	if err != nil {
		return nil, err
	}

	return &tls.Certificate{
		Certificate: [][]byte{derBytes},
		PrivateKey:  &ca.PrivateKey,
	}, nil
}
