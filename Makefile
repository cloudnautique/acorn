GO_TAGS ?= netgo
build:
	CGO_ENABLED=0 go build -o bin/acorn -tags "${GO_TAGS}" -ldflags "-s -w" .

tidy:
	go mod tidy

dev-reset: build
	docker build -t localdev .
	ACORN_IMAGE=localdev ACORN_LOCAL_PORT=6442 ./bin/acorn local start --delete

dev-install:
	[ -e .dev-image ] && go mod vendor ; go run main.go install --dev "$$(cat .dev-image)"; rm -rf vendor

generate:
	go generate

mocks:
	go run github.com/golang/mock/mockgen --build_flags=--mod=mod -destination=./pkg/mocks/mock_client.go -package=mocks github.com/acorn-io/runtime/pkg/client Client,ProjectClientFactory

image:
	docker build .

setup-ci-image:
	docker build -t acorn:v-ci .
	docker save acorn:v-ci | docker exec -i $$(docker ps | grep k3s | awk '{print $$1}') ctr --address /run/k3s/containerd/containerd.sock images import -

GOLANGCI_LINT_VERSION ?= v1.54.2
setup-env: 
	if ! command -v golangci-lint &> /dev/null; then \
  		echo "Could not find golangci-lint, installing version $(GOLANGCI_LINT_VERSION)."; \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $$(go env GOPATH)/bin $(GOLANGCI_LINT_VERSION); \
	fi

lint: setup-env
	golangci-lint run

# Run all validators
validate: validate-code validate-docs

# Runs linters and validates that all generated code is committed.
validate-code: tidy generate lint gen-docs
	if [ -n "$$(git status --porcelain)" ]; then \
		git status --porcelain; \
		echo "Encountered dirty repo!"; \
		git diff; \
		exit 1 \
	;fi

GOTESTSUM_VERSION ?= v1.10.0
GOTESTSUM ?= go run gotest.tools/gotestsum@$(GOTESTSUM_VERSION) --format testname $(TEST_FLAGS) -- $(GO_TEST_FLAGS)

.PHONY: test
test: unit integration

.PHONY: unit
unit:
	$(GOTESTSUM) $$(go list ./... | grep -v /integration/)

.PHONY: integration
integration:
	$(GOTESTSUM) ./integration/...


goreleaser:
	goreleaser build --snapshot --single-target --rm-dist

# This will initialize the node_modules needed to run the docs dev server. Run this before running serve-docs
init-docs:
	docker run --rm --workdir=/docs -v $${PWD}/docs:/docs node:18-buster yarn install

# Ensure docs build without errors. Makes sure generated docs are in-sync with CLI.
validate-docs:
	docker run --rm --workdir=/docs -v $${PWD}/docs:/docs node:18-buster yarn build
	go run tools/gendocs/main.go
	if [ -n "$$(git status --porcelain --untracked-files=no)" ]; then \
		git status --porcelain --untracked-files=no; \
		echo "Encountered dirty repo!"; \
		git diff; \
		exit 1 \
	;fi

# Launch development server for the docs site
serve-docs:
	acorn dev ./docs

gen-docs:
	go run tools/gendocs/main.go

#cut a new version for release with items in docs/docs
gen-docs-release:
	if [ -z ${version} ]; then \
  			echo "version not set (version=x.x)"; \
    		exit 1 \
    	;fi
	if [ -z ${prev-version} ]; then \
  			echo "prev-version not set (prev-version=x.x)"; \
    		exit 1 \
    	;fi
	make gen-docs
	docker run --rm --workdir=/docs -v $${PWD}/docs:/docs node:18-buster yarn docusaurus docs:version ${version}
	awk '/versions/&& ++c == 1 {print;print "\t\t\t\"${prev-version}\": {label: \"${prev-version}\", banner: \"none\", path: \"${prev-version}\"},";next}1' ./docs/docusaurus.config.js > tmp.config.js && mv tmp.config.js ./docs/docusaurus.config.js

# Deprecate a specific docs version (will still be included within docs dropdown)
deprecate-docs-version:
	if [ -z ${version} ]; then \
  			echo "version not set (version=x.x)"; \
    		exit 1 \
    	;fi
	echo "deprecating ${version} from documentation"
	grep -v '"${version}": {label: "${version}", banner: "none", path: "${version}"},' ./docs/docusaurus.config.js  > tmp.config.js && mv tmp.config.js ./docs/docusaurus.config.js

# Completely remove doc version from docs site
remove-docs-version:
	if [ -z ${version} ]; then \
  			echo "version not set (version=x.x)"; \
    		exit 1 \
    	;fi
	echo "removing ${version} from documentation completely"
	-rm  "./docs/versioned_sidebars/version-${version}-sidebars.json"
	-rm  -r ./docs/versioned_docs/version-${version}
	jq 'del(.[] | select(. == "${version}"))' ./docs/versions.json > tmp.json && mv tmp.json ./docs/versions.json
	grep -v '"${version}": {label: "${version}", banner: "none", path: "${version}"},' ./docs/docusaurus.config.js  > tmp.config.js && mv tmp.config.js ./docs/docusaurus.config.js
