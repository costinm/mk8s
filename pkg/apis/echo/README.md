# Experimental apiserver extension

Needs a separate 'go.work' file to make sure the deps are held back - apiserver won't compile with recent versions of some deps (CEL, prometheus for initial attempt).

Currently just trying to find the minimal code to start an echo server, no storage.

The goal is to run the echo-k8s-api-server with a sidecar or serverless, so no certs either - just the most minimal code I can write.

The sample code in k8s is far too complex - has storage, several resources and a LOT of boilerplate. 
