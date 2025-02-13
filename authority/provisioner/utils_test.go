package provisioner

import (
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/pkg/errors"
	"github.com/smallstep/cli/crypto/randutil"
	"github.com/smallstep/cli/jose"
)

var testAudiences = Audiences{
	Sign:   []string{"https://ca.smallstep.com/sign", "https://ca.smallstep.com/1.0/sign"},
	Revoke: []string{"https://ca.smallstep.com/revoke", "https://ca.smallstep.com/1.0/revoke"},
}

const awsTestCertificate = `-----BEGIN CERTIFICATE-----
MIICFTCCAX6gAwIBAgIRAKmbVVYAl/1XEqRfF3eJ97MwDQYJKoZIhvcNAQELBQAw
GDEWMBQGA1UEAxMNQVdTIFRlc3QgQ2VydDAeFw0xOTA0MjQyMjU3MzlaFw0yOTA0
MjEyMjU3MzlaMBgxFjAUBgNVBAMTDUFXUyBUZXN0IENlcnQwgZ8wDQYJKoZIhvcN
AQEBBQADgY0AMIGJAoGBAOHMmMXwbXN90SoRl/xXAcJs5TacaVYJ5iNAVWM5KYyF
+JwqYuJp/umLztFUi0oX0luu3EzD4KurVeUJSzZjTFTX1d/NX6hA45+bvdSUOcgV
UghO+2uhBZ4SNFxFRZ7SKvoWIN195l5bVX6/60Eo6+kUCKCkyxW4V/ksWzdXjHnf
AgMBAAGjXzBdMA4GA1UdDwEB/wQEAwIBBjASBgNVHRMBAf8ECDAGAQH/AgEBMB0G
A1UdDgQWBBRHfLOjEddK/CWCIHNg8Oc/oJa1IzAYBgNVHREEETAPgg1BV1MgVGVz
dCBDZXJ0MA0GCSqGSIb3DQEBCwUAA4GBAKNCiVM9eGb9dW2xNyHaHAmmy7ERB2OJ
7oXHfLjooOavk9lU/Gs2jfX/JSBa84+DzWg9ShmCNLti8CxU/dhzXW7jE/5CcdTa
DCA6B3Yl5TmfG9+D9dtFqRB2CiMgNcsJJE5Dc6pDwBIiSj/MkE0AaGVQmSwn6Cb6
vX1TAxqeWJHq
-----END CERTIFICATE-----`

const awsTestKey = `-----BEGIN RSA PRIVATE KEY-----
MIICXAIBAAKBgQDhzJjF8G1zfdEqEZf8VwHCbOU2nGlWCeYjQFVjOSmMhficKmLi
af7pi87RVItKF9JbrtxMw+Crq1XlCUs2Y0xU19XfzV+oQOOfm73UlDnIFVIITvtr
oQWeEjRcRUWe0ir6FiDdfeZeW1V+v+tBKOvpFAigpMsVuFf5LFs3V4x53wIDAQAB
AoGADZQFF9oWatyFCHeYYSdGRs/PlNIhD3h262XB/L6CPh4MTi/KVH01RAwROstP
uPvnvXWtb7xTtV8PQj+l0zZzb4W/DLCSBdoRwpuNXyffUCtbI22jPupTsVu+ENWR
3x7HHzoZYjU45ADSTMxEtwD7/zyNgpRKjIA2HYpkt+fI27ECQQD5/AOr9/yQD73x
cquF+FWahWgDL25YeMwdfe1HfpUxUxd9kJJKieB8E2BtBAv9XNguxIBpf7VlAKsF
NFhdfWFHAkEA5zuX8vqDecSzyNNEQd3tugxt1pGOXNesHzuPbdlw3ppN9Rbd93an
uU2TaAvTjr/3EkxulYNRmHs+RSVK54+uqQJAKWurhBQMAibJlzcj2ofiTz8pk9WJ
GBmz4HMcHMuJlumoq8KHqtgbnRNs18Ni5TE8FMu0Z0ak3L52l98rgRokQwJBAJS8
9KTLF79AFBVeME3eH4jJbe3TeyulX4ZHnZ8fe0b1IqhAqU8A+CpuCB+pW9A7Ewam
O4vZCKd4vzljH6eL+OECQHHxhYoTW7lFpKGnUDG9fPZ3eYzWpgka6w1vvBk10BAu
6fbwppM9pQ7DPMg7V6YGEjjT0gX9B9TttfHxGhvtZNQ=
-----END RSA PRIVATE KEY-----`

func must(args ...interface{}) []interface{} {
	if l := len(args); l > 0 && args[l-1] != nil {
		if err, ok := args[l-1].(error); ok {
			panic(err)
		}
	}
	return args
}

func generateJSONWebKey() (*jose.JSONWebKey, error) {
	jwk, err := jose.GenerateJWK("EC", "P-256", "ES256", "sig", "", 0)
	if err != nil {
		return nil, err
	}
	fp, err := jwk.Thumbprint(crypto.SHA256)
	if err != nil {
		return nil, err
	}
	jwk.KeyID = string(hex.EncodeToString(fp))
	return jwk, nil
}

func generateJSONWebKeySet(n int) (jose.JSONWebKeySet, error) {
	var keySet jose.JSONWebKeySet
	for i := 0; i < n; i++ {
		key, err := generateJSONWebKey()
		if err != nil {
			return jose.JSONWebKeySet{}, err
		}
		keySet.Keys = append(keySet.Keys, *key)
	}
	return keySet, nil
}

func encryptJSONWebKey(jwk *jose.JSONWebKey) (*jose.JSONWebEncryption, error) {
	b, err := json.Marshal(jwk)
	if err != nil {
		return nil, err
	}
	salt, err := randutil.Salt(jose.PBKDF2SaltSize)
	if err != nil {
		return nil, err
	}
	opts := new(jose.EncrypterOptions)
	opts.WithContentType(jose.ContentType("jwk+json"))
	recipient := jose.Recipient{
		Algorithm:  jose.PBES2_HS256_A128KW,
		Key:        []byte("password"),
		PBES2Count: jose.PBKDF2Iterations,
		PBES2Salt:  salt,
	}
	encrypter, err := jose.NewEncrypter(jose.DefaultEncAlgorithm, recipient, opts)
	if err != nil {
		return nil, err
	}
	return encrypter.Encrypt(b)
}

func decryptJSONWebKey(key string) (*jose.JSONWebKey, error) {
	enc, err := jose.ParseEncrypted(key)
	if err != nil {
		return nil, err
	}
	b, err := enc.Decrypt([]byte("password"))
	if err != nil {
		return nil, err
	}
	jwk := new(jose.JSONWebKey)
	if err := json.Unmarshal(b, jwk); err != nil {
		return nil, err
	}
	return jwk, nil
}

func generateJWK() (*JWK, error) {
	name, err := randutil.Alphanumeric(10)
	if err != nil {
		return nil, err
	}
	jwk, err := generateJSONWebKey()
	if err != nil {
		return nil, err
	}
	jwe, err := encryptJSONWebKey(jwk)
	if err != nil {
		return nil, err
	}
	public := jwk.Public()
	encrypted, err := jwe.CompactSerialize()
	if err != nil {
		return nil, err
	}
	claimer, err := NewClaimer(nil, globalProvisionerClaims)
	if err != nil {
		return nil, err
	}
	return &JWK{
		Name:         name,
		Type:         "JWK",
		Key:          &public,
		EncryptedKey: encrypted,
		Claims:       &globalProvisionerClaims,
		audiences:    testAudiences,
		claimer:      claimer,
	}, nil
}

func generateOIDC() (*OIDC, error) {
	name, err := randutil.Alphanumeric(10)
	if err != nil {
		return nil, err
	}
	clientID, err := randutil.Alphanumeric(10)
	if err != nil {
		return nil, err
	}
	issuer, err := randutil.Alphanumeric(10)
	if err != nil {
		return nil, err
	}
	jwk, err := generateJSONWebKey()
	if err != nil {
		return nil, err
	}
	claimer, err := NewClaimer(nil, globalProvisionerClaims)
	if err != nil {
		return nil, err
	}
	return &OIDC{
		Name:                  name,
		Type:                  "OIDC",
		ClientID:              clientID,
		ConfigurationEndpoint: "https://example.com/.well-known/openid-configuration",
		Claims:                &globalProvisionerClaims,
		configuration: openIDConfiguration{
			Issuer:    issuer,
			JWKSetURI: "https://example.com/.well-known/jwks",
		},
		keyStore: &keyStore{
			keySet: jose.JSONWebKeySet{Keys: []jose.JSONWebKey{*jwk}},
			expiry: time.Now().Add(24 * time.Hour),
		},
		claimer: claimer,
	}, nil
}

func generateGCP() (*GCP, error) {
	name, err := randutil.Alphanumeric(10)
	if err != nil {
		return nil, err
	}
	serviceAccount, err := randutil.Alphanumeric(10)
	if err != nil {
		return nil, err
	}
	jwk, err := generateJSONWebKey()
	if err != nil {
		return nil, err
	}
	claimer, err := NewClaimer(nil, globalProvisionerClaims)
	if err != nil {
		return nil, err
	}
	return &GCP{
		Type:            "GCP",
		Name:            name,
		ServiceAccounts: []string{serviceAccount},
		Claims:          &globalProvisionerClaims,
		claimer:         claimer,
		config:          newGCPConfig(),
		keyStore: &keyStore{
			keySet: jose.JSONWebKeySet{Keys: []jose.JSONWebKey{*jwk}},
			expiry: time.Now().Add(24 * time.Hour),
		},
		audiences: testAudiences.WithFragment("gcp/" + name),
	}, nil
}

func generateAWS() (*AWS, error) {
	name, err := randutil.Alphanumeric(10)
	if err != nil {
		return nil, err
	}
	accountID, err := randutil.Alphanumeric(10)
	if err != nil {
		return nil, err
	}
	claimer, err := NewClaimer(nil, globalProvisionerClaims)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode([]byte(awsTestCertificate))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("error decoding AWS certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, errors.Wrap(err, "error parsing AWS certificate")
	}
	return &AWS{
		Type:     "AWS",
		Name:     name,
		Accounts: []string{accountID},
		Claims:   &globalProvisionerClaims,
		claimer:  claimer,
		config: &awsConfig{
			identityURL:        awsIdentityURL,
			signatureURL:       awsSignatureURL,
			certificate:        cert,
			signatureAlgorithm: awsSignatureAlgorithm,
		},
		audiences: testAudiences.WithFragment("aws/" + name),
	}, nil
}

func generateAWSWithServer() (*AWS, *httptest.Server, error) {
	aws, err := generateAWS()
	if err != nil {
		return nil, nil, err
	}
	block, _ := pem.Decode([]byte(awsTestKey))
	if block == nil || block.Type != "RSA PRIVATE KEY" {
		return nil, nil, errors.New("error decoding AWS key")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, nil, errors.Wrap(err, "error parsing AWS private key")
	}
	doc, err := json.MarshalIndent(awsInstanceIdentityDocument{
		AccountID:        aws.Accounts[0],
		Architecture:     "x86_64",
		AvailabilityZone: "us-west-2b",
		ImageID:          "image-id",
		InstanceID:       "instance-id",
		InstanceType:     "t2.micro",
		PendingTime:      time.Now(),
		PrivateIP:        "127.0.0.1",
		Region:           "us-west-1",
		Version:          "2017-09-30",
	}, "", "  ")
	if err != nil {
		return nil, nil, err
	}

	sum := sha256.Sum256(doc)
	signature, err := key.Sign(rand.Reader, sum[:], crypto.SHA256)
	if err != nil {
		return nil, nil, errors.Wrap(err, "error signing document")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest/dynamic/instance-identity/document":
			w.Write(doc)
		case "/latest/dynamic/instance-identity/signature":
			w.Write([]byte(base64.StdEncoding.EncodeToString(signature)))
		case "/bad-document":
			w.Write([]byte("{}"))
		case "/bad-signature":
			w.Write([]byte("YmFkLXNpZ25hdHVyZQo="))
		case "/bad-json":
			w.Write([]byte("{"))
		default:
			http.NotFound(w, r)
		}
	}))
	aws.config.identityURL = srv.URL + "/latest/dynamic/instance-identity/document"
	aws.config.signatureURL = srv.URL + "/latest/dynamic/instance-identity/signature"
	return aws, srv, nil
}

func generateAzure() (*Azure, error) {
	name, err := randutil.Alphanumeric(10)
	if err != nil {
		return nil, err
	}
	tenantID, err := randutil.Alphanumeric(10)
	if err != nil {
		return nil, err
	}
	claimer, err := NewClaimer(nil, globalProvisionerClaims)
	if err != nil {
		return nil, err
	}
	jwk, err := generateJSONWebKey()
	if err != nil {
		return nil, err
	}
	return &Azure{
		Type:     "Azure",
		Name:     name,
		TenantID: tenantID,
		Audience: azureDefaultAudience,
		Claims:   &globalProvisionerClaims,
		claimer:  claimer,
		config:   newAzureConfig(tenantID),
		oidcConfig: openIDConfiguration{
			Issuer:    "https://sts.windows.net/" + tenantID + "/",
			JWKSetURI: "https://login.microsoftonline.com/common/discovery/keys",
		},
		keyStore: &keyStore{
			keySet: jose.JSONWebKeySet{Keys: []jose.JSONWebKey{*jwk}},
			expiry: time.Now().Add(24 * time.Hour),
		},
	}, nil
}

func generateAzureWithServer() (*Azure, *httptest.Server, error) {
	az, err := generateAzure()
	if err != nil {
		return nil, nil, err
	}
	writeJSON := func(w http.ResponseWriter, v interface{}) {
		b, err := json.Marshal(v)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(b)
	}
	getPublic := func(ks jose.JSONWebKeySet) jose.JSONWebKeySet {
		var ret jose.JSONWebKeySet
		for _, k := range ks.Keys {
			ret.Keys = append(ret.Keys, k.Public())
		}
		return ret
	}
	issuer := "https://sts.windows.net/" + az.TenantID + "/"
	srv := httptest.NewUnstartedServer(nil)
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/error":
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		case "/" + az.TenantID + "/.well-known/openid-configuration":
			writeJSON(w, openIDConfiguration{Issuer: issuer, JWKSetURI: srv.URL + "/jwks_uri"})
		case "/openid-configuration-no-issuer":
			writeJSON(w, openIDConfiguration{Issuer: "", JWKSetURI: srv.URL + "/jwks_uri"})
		case "/openid-configuration-fail-jwk":
			writeJSON(w, openIDConfiguration{Issuer: issuer, JWKSetURI: srv.URL + "/error"})
		case "/random":
			keySet := must(generateJSONWebKeySet(2))[0].(jose.JSONWebKeySet)
			w.Header().Add("Cache-Control", "max-age=5")
			writeJSON(w, getPublic(keySet))
		case "/private":
			writeJSON(w, az.keyStore.keySet)
		case "/jwks_uri":
			w.Header().Add("Cache-Control", "max-age=5")
			writeJSON(w, getPublic(az.keyStore.keySet))
		case "/metadata/identity/oauth2/token":
			tok, err := generateAzureToken("subject", issuer, "https://management.azure.com/", az.TenantID, "subscriptionID", "resourceGroup", "virtualMachine", time.Now(), &az.keyStore.keySet.Keys[0])
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			} else {
				writeJSON(w, azureIdentityToken{
					AccessToken: tok,
				})
			}
		default:
			http.NotFound(w, r)
		}
	})
	srv.Start()
	az.config.oidcDiscoveryURL = srv.URL + "/" + az.TenantID + "/.well-known/openid-configuration"
	az.config.identityTokenURL = srv.URL + "/metadata/identity/oauth2/token"
	return az, srv, nil
}

func generateCollection(nJWK, nOIDC int) (*Collection, error) {
	col := NewCollection(testAudiences)
	for i := 0; i < nJWK; i++ {
		p, err := generateJWK()
		if err != nil {
			return nil, err
		}
		col.Store(p)
	}
	for i := 0; i < nOIDC; i++ {
		p, err := generateOIDC()
		if err != nil {
			return nil, err
		}
		col.Store(p)
	}
	return col, nil
}

func generateSimpleToken(iss, aud string, jwk *jose.JSONWebKey) (string, error) {
	return generateToken("subject", iss, aud, "name@smallstep.com", []string{"test.smallstep.com"}, time.Now(), jwk)
}

func generateToken(sub, iss, aud string, email string, sans []string, iat time.Time, jwk *jose.JSONWebKey) (string, error) {
	sig, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.ES256, Key: jwk.Key},
		new(jose.SignerOptions).WithType("JWT").WithHeader("kid", jwk.KeyID),
	)
	if err != nil {
		return "", err
	}

	id, err := randutil.ASCII(64)
	if err != nil {
		return "", err
	}

	claims := struct {
		jose.Claims
		Email string   `json:"email"`
		SANS  []string `json:"sans"`
	}{
		Claims: jose.Claims{
			ID:        id,
			Subject:   sub,
			Issuer:    iss,
			IssuedAt:  jose.NewNumericDate(iat),
			NotBefore: jose.NewNumericDate(iat),
			Expiry:    jose.NewNumericDate(iat.Add(5 * time.Minute)),
			Audience:  []string{aud},
		},
		Email: email,
		SANS:  sans,
	}
	return jose.Signed(sig).Claims(claims).CompactSerialize()
}

func generateSimpleSSHUserToken(iss, aud string, jwk *jose.JSONWebKey) (string, error) {
	return generateSSHToken("subject@localhost", iss, aud, time.Now(), &SSHOptions{
		CertType:   "user",
		Principals: []string{"name"},
	}, jwk)
}

func generateSimpleSSHHostToken(iss, aud string, jwk *jose.JSONWebKey) (string, error) {
	return generateSSHToken("subject@localhost", iss, aud, time.Now(), &SSHOptions{
		CertType:   "host",
		Principals: []string{"smallstep.com"},
	}, jwk)
}

func generateSSHToken(sub, iss, aud string, iat time.Time, sshOpts *SSHOptions, jwk *jose.JSONWebKey) (string, error) {
	sig, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.ES256, Key: jwk.Key},
		new(jose.SignerOptions).WithType("JWT").WithHeader("kid", jwk.KeyID),
	)
	if err != nil {
		return "", err
	}

	id, err := randutil.ASCII(64)
	if err != nil {
		return "", err
	}

	claims := struct {
		jose.Claims
		Step *stepPayload `json:"step,omitempty"`
	}{
		Claims: jose.Claims{
			ID:        id,
			Subject:   sub,
			Issuer:    iss,
			IssuedAt:  jose.NewNumericDate(iat),
			NotBefore: jose.NewNumericDate(iat),
			Expiry:    jose.NewNumericDate(iat.Add(5 * time.Minute)),
			Audience:  []string{aud},
		},
		Step: &stepPayload{
			SSH: sshOpts,
		},
	}
	return jose.Signed(sig).Claims(claims).CompactSerialize()
}

func generateGCPToken(sub, iss, aud, instanceID, instanceName, projectID, zone string, iat time.Time, jwk *jose.JSONWebKey) (string, error) {
	sig, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.ES256, Key: jwk.Key},
		new(jose.SignerOptions).WithType("JWT").WithHeader("kid", jwk.KeyID),
	)
	if err != nil {
		return "", err
	}
	aud, err = generateSignAudience("https://ca.smallstep.com", aud)
	if err != nil {
		return "", err
	}
	claims := gcpPayload{
		Claims: jose.Claims{
			Subject:   sub,
			Issuer:    iss,
			IssuedAt:  jose.NewNumericDate(iat),
			NotBefore: jose.NewNumericDate(iat),
			Expiry:    jose.NewNumericDate(iat.Add(5 * time.Minute)),
			Audience:  []string{aud},
		},
		AuthorizedParty: sub,
		Email:           "foo@developer.gserviceaccount.com",
		EmailVerified:   true,
		Google: gcpGooglePayload{
			ComputeEngine: gcpComputeEnginePayload{
				InstanceID:                instanceID,
				InstanceName:              instanceName,
				InstanceCreationTimestamp: jose.NewNumericDate(iat),
				ProjectID:                 projectID,
				ProjectNumber:             1234567890,
				Zone:                      zone,
			},
		},
	}
	return jose.Signed(sig).Claims(claims).CompactSerialize()
}

func generateAWSToken(sub, iss, aud, accountID, instanceID, privateIP, region string, iat time.Time, key crypto.Signer) (string, error) {
	doc, err := json.MarshalIndent(awsInstanceIdentityDocument{
		AccountID:        accountID,
		Architecture:     "x86_64",
		AvailabilityZone: "us-west-2b",
		ImageID:          "ami-123123",
		InstanceID:       instanceID,
		InstanceType:     "t2.micro",
		PendingTime:      iat,
		PrivateIP:        privateIP,
		Region:           region,
		Version:          "2017-09-30",
	}, "", "  ")
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(doc)
	signature, err := key.Sign(rand.Reader, sum[:], crypto.SHA256)
	if err != nil {
		return "", errors.Wrap(err, "error signing document")
	}

	sig, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.HS256, Key: signature},
		new(jose.SignerOptions).WithType("JWT"),
	)
	if err != nil {
		return "", err
	}

	aud, err = generateSignAudience("https://ca.smallstep.com", aud)
	if err != nil {
		return "", err
	}

	claims := awsPayload{
		Claims: jose.Claims{
			Subject:   sub,
			Issuer:    iss,
			IssuedAt:  jose.NewNumericDate(iat),
			NotBefore: jose.NewNumericDate(iat),
			Expiry:    jose.NewNumericDate(iat.Add(5 * time.Minute)),
			Audience:  []string{aud},
		},
		Amazon: awsAmazonPayload{
			Document:  doc,
			Signature: signature,
		},
	}
	return jose.Signed(sig).Claims(claims).CompactSerialize()
}

func generateAzureToken(sub, iss, aud, tenantID, subscriptionID, resourceGroup, virtualMachine string, iat time.Time, jwk *jose.JSONWebKey) (string, error) {
	sig, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.ES256, Key: jwk.Key},
		new(jose.SignerOptions).WithType("JWT").WithHeader("kid", jwk.KeyID),
	)
	if err != nil {
		return "", err
	}

	claims := azurePayload{
		Claims: jose.Claims{
			Subject:   sub,
			Issuer:    iss,
			IssuedAt:  jose.NewNumericDate(iat),
			NotBefore: jose.NewNumericDate(iat),
			Expiry:    jose.NewNumericDate(iat.Add(5 * time.Minute)),
			Audience:  []string{aud},
			ID:        "the-jti",
		},
		AppID:            "the-appid",
		AppIDAcr:         "the-appidacr",
		IdentityProvider: "the-idp",
		ObjectID:         "the-oid",
		TenantID:         tenantID,
		Version:          "the-version",
		XMSMirID:         fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/virtualMachines/%s", subscriptionID, resourceGroup, virtualMachine),
	}
	return jose.Signed(sig).Claims(claims).CompactSerialize()
}

func parseToken(token string) (*jose.JSONWebToken, *jose.Claims, error) {
	tok, err := jose.ParseSigned(token)
	if err != nil {
		return nil, nil, err
	}
	claims := new(jose.Claims)
	if err := tok.UnsafeClaimsWithoutVerification(claims); err != nil {
		return nil, nil, err
	}
	return tok, claims, nil
}

func parseAWSToken(token string) (*jose.JSONWebToken, *awsPayload, error) {
	tok, err := jose.ParseSigned(token)
	if err != nil {
		return nil, nil, err
	}
	claims := new(awsPayload)
	if err := tok.UnsafeClaimsWithoutVerification(claims); err != nil {
		return nil, nil, err
	}
	var doc awsInstanceIdentityDocument
	if err := json.Unmarshal(claims.Amazon.Document, &doc); err != nil {
		return nil, nil, errors.Wrap(err, "error unmarshaling identity document")
	}
	claims.document = doc
	return tok, claims, nil
}

func generateJWKServer(n int) *httptest.Server {
	hits := struct {
		Hits int `json:"hits"`
	}{}
	writeJSON := func(w http.ResponseWriter, v interface{}) {
		b, err := json.Marshal(v)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(b)
	}
	getPublic := func(ks jose.JSONWebKeySet) jose.JSONWebKeySet {
		var ret jose.JSONWebKeySet
		for _, k := range ks.Keys {
			ret.Keys = append(ret.Keys, k.Public())
		}
		return ret
	}

	defaultKeySet := must(generateJSONWebKeySet(n))[0].(jose.JSONWebKeySet)
	srv := httptest.NewUnstartedServer(nil)
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Hits++
		switch r.RequestURI {
		case "/error":
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		case "/hits":
			writeJSON(w, hits)
		case "/openid-configuration", "/.well-known/openid-configuration":
			writeJSON(w, openIDConfiguration{Issuer: "the-issuer", JWKSetURI: srv.URL + "/jwks_uri"})
		case "/random":
			keySet := must(generateJSONWebKeySet(n))[0].(jose.JSONWebKeySet)
			w.Header().Add("Cache-Control", "max-age=5")
			writeJSON(w, getPublic(keySet))
		case "/no-cache":
			keySet := must(generateJSONWebKeySet(n))[0].(jose.JSONWebKeySet)
			w.Header().Add("Cache-Control", "no-cache, no-store, max-age=0, must-revalidate")
			writeJSON(w, getPublic(keySet))
		case "/private":
			writeJSON(w, defaultKeySet)
		default:
			w.Header().Add("Cache-Control", "max-age=5")
			writeJSON(w, getPublic(defaultKeySet))
		}
	})

	srv.Start()
	return srv
}

func generateACME() (*ACME, error) {
	// Initialize provisioners
	p := &ACME{
		Type: "ACME",
		Name: "test@acme-provisioner.com",
	}
	if err := p.Init(Config{Claims: globalProvisionerClaims}); err != nil {
		return nil, err
	}
	return p, nil
}
