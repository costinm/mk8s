package gcp

import (
	"context"
	"strconv"

	"github.com/costinm/meshauth"
	"github.com/costinm/meshauth/pkg/uk8s"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	crm "google.golang.org/api/cloudresourcemanager/v1"
)

// JSON key file types.
//const (
//	serviceAccountKey  = "service_account"
//	userCredentialsKey = "authorized_user"
//)

// Create a token source for access tokens - based on GOOGLE_APPLICATION_CREDENTIALS or MDS
// This only returns access tokens if the default credentials are for a google account.
// Best to use the fake MDS
func InitDefaultTokenSource(ctx context.Context) func(context.Context, string) (string, error) {
	ts, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		sk, err := google.NewSDKConfig("")
		if err != nil {
			return nil
		}
		ts = sk.TokenSource(ctx)
	}
	t := func(ctx context.Context, s string) (string, error) {
		t, err := ts.Token()
		if err != nil {
			return "", err
		}
		// TODO: cache, use t.Expiry
		return t.AccessToken, err
	}
	return t
}

const (
	metaPrefix = "/computeMetadata/v1"
	projIDPath = metaPrefix + "/project/project-id"
)

// GcpInit will detect google credentials or MDS, and init the MDS struct accordingly.
//
// - projectId will be populated based on credentials
// - an access token source will be populated ("gcp")
//
// DefaultTokenSource will:
// - check GOOGLE_APPLICATION_CREDENTIALS - should be downloaded service account, can produce JWTs
// - ~/.config/gcloud/application_default_credentials.json"
// - use metadata
//
// This also works for K8S, using node MDS or GKE MDS - but only if the
// ServiceAccount is annotated with a GSA (with permissions to use).
// Also specific to GKE and GCP APIs.
func GcpInit(ctx context.Context, mds *meshauth.MeshAuth, acct string) error {
	var ts oauth2.TokenSource

	// For GCP, google.ComputeTokenSource(account, scopes) is best option
	// But on GKE it doesn't work except for federated access tokens.

	// Usual pattern is to use google.DefaultTokenSource - which internally calls this.
	creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {

		// .config/gcloud/credentials and .config/gcloud/properties
		// The properties file include core/account - the default account to use.
		// credentials include a refresh token and possibly cached access token.
		sdkCfg, err := google.NewSDKConfig("")
		if err != nil {
			return err
		}
		ts = sdkCfg.TokenSource(ctx)
		mds.AuthProviders["gcp"] = meshauth.TokenSourceFunc(func(ctx context.Context, s string) (string, error) {
			t, err := ts.Token()
			if err != nil {
				return "", err
			}
			// TODO: cache, use t.Expiry
			return t.AccessToken, nil
		})

		return nil
	}

	ts = creds.TokenSource
	mds.MDS.Meta.Store(projIDPath, creds.ProjectID)

	mds.AuthProviders["gcp"] = &GCPAuthProvider{
		AccessTokenSource: ts,
		GSA:               acct,
	}
	// To get custom audience token sources:
	//creds.NewTokenSource()

	// creds.JSON may have additional info.
	// Examples:
	// {
	//   // ???
	//  "client_id": "32555940559.apps.googleusercontent.com",
	//  "client_secret": "",
	//  "refresh_token": "1//...",
	//  "type": "authorized_user"
	//}
	// CredentialsFromJSON can also parse a file.

	return nil
}

// GCPAuthProvider returns access or JWT tokens for a google account.
type GCPAuthProvider struct {
	// Returns access tokens for a user or service account (via default credentials)
	// or federated access tokens.
	AccessTokenSource oauth2.TokenSource

	// GSA to get tokens for.
	GSA string
}

func (gcp *GCPAuthProvider) GetToken(ctx context.Context, s string) (string, error) {
	if s != "" {
		// https://cloud.google.com/docs/authentication/get-id-token#go
		//
		// This has 2 problems:
		// - brings a lot of deps (grpc, etc)
		// - doesn't work with ADC except for service_account ( not user )
		// Internally this just uses jwtSource with google access tokens
		//
		//ts1, err := idtoken.NewTokenSource(ctx, s, option.WithCredentials(gcp.creds))
		//if err != nil {
		//	return "", err
		//}
		//t, err := ts1.Token()

		// jwt.TokenSource relies on a private key that is exchanged using
		//    "urn:ietf:params:oauth:grant-type:jwt-bearer"
		// Useful if we have a private key that can be used to sign the JWTs
		//ts2c := &jwt.Config{
		//	Audience:   s,
		//	UseIDToken: true,
		//	TokenURL:   "",
		//}
		//ts2 := ts2c.TokenSource(ctx)
		//
		//t2, err := ts2.Token()
		//if err != nil {
		//	return "", err
		//}

		access, err := gcp.AccessTokenSource.Token()
		meshauth.Debug = true

		gcpa := uk8s.NewGCPTokenSource(&uk8s.GCPAuthConfig{
			GSA: gcp.GSA,
		})
		t, err := gcpa.TokenGSA(ctx, access.AccessToken, s)
		return t, err
	}

	t, err := gcp.AccessTokenSource.Token()
	if err != nil {
		return "", err
	}

	// TODO: cache, use t.Expiry
	return t.AccessToken, nil
}

func ProjectLabels(ctx context.Context, p string) (map[string]string, error) {
	cr, err := crm.NewService(ctx)
	if err != nil {
		return nil, err
	}
	pdata, err := cr.Projects.Get(p).Context(ctx).Do()
	if err != nil {
		return nil, err
	}

	return pdata.Labels, nil
}

func ProjectNumber(p string) string {
	ctx := context.Background()

	cr, err := crm.NewService(ctx)
	if err != nil {
		return ""
	}
	pdata, err := cr.Projects.Get(p).Do()
	if err != nil {
		return ""
	}

	// This is in v1 - v3 has it encoded in name.
	return strconv.Itoa(int(pdata.ProjectNumber))
}
