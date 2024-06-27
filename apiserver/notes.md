
- make sure go list ./... works

# Generation

Use default paths
controller-gen  +paths=./... object crd 
register-gen ./...
- Required
openapi-gen ./... --output-dir . --output-pkg github.com/costinm/mk8s/apiserver/openapi
go-to-protobu

# Apiserver-boot

mkdir -p $HOME/go/src/github.com/costinm
ln -s /ws/mk8s/echoapiserver $HOME/go/src/github.com/costinm/echoapiserver

apiserver-boot init repo --domain costinm.github.com

apiserver-boot create group version resource --group <your-group> --version <your-version> --kind <your-kind>

apt install etcd-server etcd-cli

Notes:
- generates a lot of stuff, including a manager


apiserver-runtime is based on 0.23 - not 30.2, last commit 2023 - not up to date.

Main feature is the 'builder' which sets the config for API server, by adding wrappers around RecommendedOptions.
