.PHONY: iam-db test-iam test-iam-coverage test-iam-integration update-shared-packages update-shared-packages-deps


test-iam:
	go test -v ./services/iam/...


test-iam-coverage:
	go test -v -coverprofile=services/iam/coverage.out ./services/iam/...


test-iam-integration: iam-db
	IAM_TEST_DATABASE_URL="postgres://postgres:postgres@localhost:5433/postgres?sslmode=disable" \
		go test -v -tags=integration ./services/iam/...


iam-db:
	@docker compose up -d --wait iam_database


SERVICES := $(patsubst %/,%,$(wildcard services/*/))
REPO_LINK := github.com/ikaelfess/distributed-workflow
define update_pkg
GOPROXY=direct go get $(REPO_LINK)/pkg/httpserver@latest; \
GOPROXY=direct go get $(REPO_LINK)/pkg/shutdown@latest; \
GOPROXY=direct go get $(REPO_LINK)/pkg/logger@latest; \
GOPROXY=direct go get $(REPO_LINK)/pkg/database@latest
endef
update-shared-packages:
	@for svc in $(SERVICES); do \
		echo "==> $$svc"; \
		( \
			cd $$svc && \
			$(update_pkg) \
		) || exit $$?; \
	done


SHARED_PACKAGES := $(patsubst %/,%,$(wildcard pkg/*/))
update-shared-packages-deps:
	@for svc in $(SHARED_PACKAGES); do \
		echo "==> $$svc"; \
		( \
			cd $$svc && \
			go get -u ./... && \
			go mod tidy \
		) || exit $$?; \
	done
