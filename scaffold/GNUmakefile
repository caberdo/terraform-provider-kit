default: build

BINARY_NAME=terraform-provider-{{PROVIDER}}
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

# The shared tooling for the caberdo providers: the coverage gate, merge and
# README targets, example validation, the release-metadata check and the
# scaffold check. Keep this pin equal to the `uses:` refs in
# .github/workflows/*.yml: one version for the local targets and the reusable
# workflows, so they cannot drift apart.
#
# Install the pinned kit once and call the binary. `go run` rewrites a child's
# non-zero status to 1, which would hide the difference between drift (1) and a
# configuration problem (2) that `make scaffold-check` and the coverage commands
# document. Override TFKIT to use a binary you installed yourself.
TFKIT_PACKAGE = github.com/caberdo/terraform-provider-kit/cmd/tfkit@{{KIT_VERSION}}
ifeq ($(origin TFKIT), undefined)
TFKIT := $(CURDIR)/bin/tfkit
TFKIT_DEP := $(TFKIT)
endif

$(CURDIR)/bin/tfkit:
	@mkdir -p $(dir $@)
	GOBIN=$(dir $@) go install $(TFKIT_PACKAGE)

build:
	go build -o bin/$(BINARY_NAME) -ldflags "-X main.version=$(VERSION)" .

test:
	go test -v -cover {{PACKAGES}}

# Unit coverage only: `TestAcc*` needs TF_ACC and a reachable {{PRODUCT}} server,
# so it is skipped to keep the profile independent of the environment.
coverage:
	go test -count=1 -skip '^TestAcc' -covermode=atomic -coverprofile=coverage.out {{PACKAGES}}
	@echo
	@go tool cover -func=coverage.out

coverage-html: coverage
	go tool cover -html=coverage.out

# Unit coverage (coverage.out) plus the acceptance profile written by the
# acceptance container (coverage-acceptance.out) as one number.
coverage-merge: $(TFKIT_DEP)
	$(TFKIT) coverage merge --output coverage-union.out coverage.out coverage-acceptance.out
	@echo
	@go tool cover -func=coverage-union.out

# Regenerate the coverage badge and floor statement in README.md from
# .github/coverage-baseline.txt; `make coverage-readme-check` fails when the
# committed README has drifted from the baseline.
coverage-readme: $(TFKIT_DEP)
	$(TFKIT) coverage readme

coverage-readme-check: $(TFKIT_DEP)
	$(TFKIT) coverage readme --check

# Fail when the merged profile is below the floor in .github/coverage-baseline.txt.
coverage-gate: $(TFKIT_DEP)
	$(TFKIT) coverage gate --profile coverage-union.out

testacc:
	TF_ACC=1 go test -v -cover {{PACKAGES}}

testacc-docker:
	docker compose run --rm test

lint:
	golangci-lint run --config .golangci.yml ./...

lint-fix:
	golangci-lint run --config .golangci.yml --fix ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

install: build
	mkdir -p ~/.terraform.d/plugins/registry.terraform.io/{{REGISTRY_NAMESPACE}}/{{PROVIDER}}/$(VERSION)/$$(go env GOOS)_$$(go env GOARCH)
	cp bin/$(BINARY_NAME) ~/.terraform.d/plugins/registry.terraform.io/{{REGISTRY_NAMESPACE}}/{{PROVIDER}}/$(VERSION)/$$(go env GOOS)_$$(go env GOARCH)/$(BINARY_NAME)

# Validate every Terraform example against the provider built from this tree.
# tfkit builds into a temporary directory; set TFKIT_PLUGIN_DIR (or pass
# --plugin-dir) to pin that directory, so parallel runs do not overwrite each
# other's build.
validate-examples: $(TFKIT_DEP)
	$(TFKIT) examples validate --provider {{PROVIDER}}

generate:
	go generate ./...

# Fail when the changelog and the README disagree with the released version.
# The released version is the last git tag, so there is no file to keep in sync.
# The tag workflow enforces the same agreement on main; this keeps the failure
# visible before the next release instead of after it.
release-check: $(TFKIT_DEP)
	$(TFKIT) release check

# Fail when a file the kit owns has drifted from the canonical scaffold. The
# provider, module and versions-file are derived from the repository; the
# product-facing facts are named here. `--allow <path>:<reason>` records a
# deliberate deviation instead of hiding it.
scaffold-check: $(TFKIT_DEP)
	$(TFKIT) scaffold check \
		--product {{PRODUCT}} \
		--year {{YEAR}} \
		--copyright "{{COPYRIGHT}}" \
		--kit-version {{KIT_VERSION}} \
		--packages {{PACKAGES}} \
		--upstream {{UPSTREAM_URL}} \
		--repo {{REPO}} \
		--license {{LICENSE}}

.PHONY: default build test coverage coverage-html coverage-merge coverage-readme coverage-readme-check coverage-gate testacc testacc-docker lint lint-fix fmt vet install validate-examples generate release-check scaffold-check
