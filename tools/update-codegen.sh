#!/usr/bin/env bash

# Copyright 2020 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -o errexit
set -o nounset
set -o pipefail

# GIT repo
GIT=${PKG:-github.com/costinm}

# GIT PKG (top level)
GITPKG=${PKG:-$GIT/mk8s}

# Top level GIT package
export GITPKG

# Script in tools
readonly SCRIPT_ROOT="$(cd "$(dirname "${BASH_SOURCE}")" && pwd)"
readonly BASE_ROOT="$(cd "$(dirname "${BASH_SOURCE}")"/.. && pwd)"

readonly COMMON_FLAGS=

# Keep outer module cache so we don't need to redownload them each time.
# The build cache already is persisted.
readonly GOMODCACHE="$(go env GOMODCACHE)"
readonly GO111MODULE="on"


export GOMODCACHE GO111MODULE GOFLAGS GOPATH

# Even when modules are enabled, the code-generator tools always write to
# a traditional GOPATH directory, so fake on up to point to the current
# workspace.
#readonly GOPATH="/tmp/GOPATH"
#readonly GOPATH="$(mktemp -d)"
#mkdir -p "$GOPATH/src/$GIT"
#ln -s  $BASE_ROOT "$GOPATH/src/$GIT" || true


API=${API:-echo}
export PKG=${GITPKG}/${API}
export API

readonly OUTPUT_PKG=$PKG/client
readonly APIS_PKG=$PKG

# Output:
# - cachedclient - for lister, informer
# - client - for the client (not cached)
# - pkg/server - for server side code

OUT="cachedclient"

install() {
    go install k8s.io/code-generator/cmd/lister-gen
    go install k8s.io/code-generator/cmd/client-gen
    go install k8s.io/code-generator/cmd/applyconfiguration-gen
    go install k8s.io/code-generator/cmd/informer-gen

    go install k8s.io/kube-openapi/cmd/openapi-gen
    go install k8s.io/code-generator/cmd/go-to-protobuf
    go install k8s.io/code-generator/cmd/deepcopy-gen

  go install sigs.k8s.io/controller-tools/cmd/controller-gen
  go install sigs.k8s.io/controller-tools/cmd/type-scaffold
  go install k8s.io/code-generator/cmd/register-gen

  #go install sigs.k8s.io/apiserver-builder-alpha/cmd/apiserver-boot@v1.23.0

}

# Generate REST wrapper - not cached.
client() {
  local P=$1

  # TODO: --apply-configuration-package
  # Default clientset-name - internalclientset - it corresponds to 'versioned-clientset-package' in informer-gen
  # clientset-only - no typed clients
  # input-base defaults to "k8s.io/kubernetes/pkg/apis
  # input - gruop/version1,group/version2
  client-gen \
    --fake-clientset=false \
    --clientset-name "clientset" \
    --input-base ${GITPKG} \
    --output-dir ./${P} \
    --input $P/v1 \
     --output-pkg ${GITPKG}/${P}
}

listers() {
    local P=$1

  # Generates echo/v1 under the output directory.
  lister-gen  \
  --output-pkg "$GITPKG/$P/cached/listers" \
  --output-dir "./$P/cached/listers" \
  ${COMMON_FLAGS} \
     "./$P/v1"
}

informers() {
  local P=$1

  # Require the listers, about same depenencies.
  listers $P

  # Generates externalversions/echo/v1 and externalversions/internalinterfaces under the output directory.
  informer-gen \
    --output-dir "./$P/cached/informers" \
    --listers-package "$GITPKG/$P/cached/listers" \
    --versioned-clientset-package "$GITPKG/$P/clientset" \
    --output-pkg "$GITPKG/$P/cached/informers" \
    ${COMMON_FLAGS} \
       "./$P/v1"

}

openapi() {
  local P=$1
  # Generates generated.openapi.go in package openapi in the output directory.
  # Triggered by +k8s:openapi-gen=true
  openapi-gen \
    --output-dir "./openapi/$P" \
    --output-pkg "$GITPKG/openapi/$P" ${COMMON_FLAGS} \
     ./$P/v1
}

codegen() {
  local P=$1

  deepcopy-gen ./$P/v1

  register-gen  ./$P/v1

  #controller-gen +object \
  #                    paths=./$P/v1

  # Default: ./config/crd/...
  controller-gen +crd \
                  output:crd:dir=../manifests/charts/$P/crds \
                        paths=./$P/v1
}

all() {
  local P=$1

  client $P

  listers $P
  informers $P
  openapi $P

  codegen $P
}

all $1
