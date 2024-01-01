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
GITPKG=${PKG:-$GIT/uk8s}

export GITPKG

readonly SCRIPT_ROOT="$(cd "$(dirname "${BASH_SOURCE}")"/.. && pwd)"
readonly BASE_ROOT="$(cd "$(dirname "${BASH_SOURCE}")"/../.. && pwd)"

readonly COMMON_FLAGS="${VERIFY_FLAG:-} --go-header-file ${SCRIPT_ROOT}/hack/boilerplate/boilerplate.generatego.txt"


# Keep outer module cache so we don't need to redownload them each time.
# The build cache already is persisted.
readonly GOMODCACHE="$(go env GOMODCACHE)"
readonly GO111MODULE="on"
readonly GOPATH="$(mktemp -d)"

export GOMODCACHE GO111MODULE GOFLAGS GOPATH

# Even when modules are enabled, the code-generator tools always write to
# a traditional GOPATH directory, so fake on up to point to the current
# workspace.
mkdir -p "$GOPATH/src/$GIT"

ln -s  $BASE_ROOT "$GOPATH/src/$GIT"


API=${API:-echo}
export PKG=${GITPKG}/${API}
export API


readonly OUTPUT_PKG=$PKG/client
readonly APIS_PKG=$PKG


go run k8s.io/code-generator/cmd/lister-gen  \
  --input-dirs "${APIS_PKG}/apis/v1" \
  --output-package "$GITPKG/cachedclient/listers" \
  ${COMMON_FLAGS}

go run k8s.io/code-generator/cmd/informer-gen \
  --input-dirs "${APIS_PKG}/apis/v1" \
  --versioned-clientset-package "$GITPKG/client/clientset/versioned" \
  --listers-package "$GITPKG/cachedclient/listers" \
  --output-package "$GITPKG/cachedclient/informers" \
  ${COMMON_FLAGS}

go run k8s.io/code-generator/cmd/openapi-gen \
  --input-dirs "${APIS_PKG}/apis/v1" \
  --output-package "$GITPKG/cachedclient/openapi" ${COMMON_FLAGS}
