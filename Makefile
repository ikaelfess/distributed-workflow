REPO_LINK := github.com/ikaelfess/distributed-workflow
SERVICES := services/iam

.PHONY: test-iam test-iam-coverage update-all-packages

test-iam:
	go test -v ./services/iam/...

test-iam-coverage:
	go test -v -coverprofile=services/iam/coverage.out ./services/iam/...

iam-db:
	@docker-compose up -d iam_database

define update_packages
GOPROXY=direct go get ${REPO_LINK}/pkg/httpserver@latest; \
GOPROXY=direct go get ${REPO_LINK}/pkg/shutdown@latest; \
GOPROXY=direct go get ${REPO_LINK}/pkg/logger@latest; \
GOPROXY=direct go get ${REPO_LINK}/pkg/database@latest
endef

# Update packages in all services
update-all-packages:
	@for svc in $(SERVICES); do \
		echo "==> $$svc"; \
		( \
			cd $$svc && \
			$(update_packages) \
		) || exit $$?; \
	done
