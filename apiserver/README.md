# Minimal K8S APIserver and extension server.

This is intended as the most minimal working API server. Only one CRD - echo.

No storage or other features - it is expected that a real APIserver is used (using this
as an extension) or a Gateway and secure environment provies authz/authn/TLS.

Original attempt used sigs.k8s.io/controller-runtime and sigs.k8s.io/apiserver-runtime, 
but they add too much (badly) opinionated abstraction, deps and seem to be behind.

## Notes

The main pain point is that it doesn't seem possible to start without mTLS, even if the
server is supposed to run in a secure environment (ambient, cloudrun, etc).

The second pain is the upgrade - still far too many deps that break.
