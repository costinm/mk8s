# Auth and bootstrap for GCP

This is a separate module, with dependencies to GCP APIs related
to authentication, getting secrets and auto config.

The meshauth and mk8s packages have min deps and provide zero-deps REST alternatives 
for some of this code. This package provides the integration using official library.

The identity is based on:
- GOOGLE_APPLICATION_CREDENTIALS
- well known file ~/.config/gcloud/application_default_credentials.json
- if MDS is detected - GCE_METADATA_HOST, 169.254.169.254 and metadata.google.internal (2 sec timeout !)


## Dependencies

golang.org/x/oauth2 - which in turn depends on gcp/metadata, protobuf
  This is the main Oauth2 library for go.

