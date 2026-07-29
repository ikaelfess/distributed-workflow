REPO_LINK := github.com/ikaelfess/distributed-workflow
SERVICES := services/iam
SHARED_PACKAGES := pkg/database pkg/httpserver pkg/logger pkg/shutdown

# Func to update shared packages for one service under services/*
define update_pkg
GOPROXY=direct go get ${REPO_LINK}/pkg/httpserver@latest; \
GOPROXY=direct go get ${REPO_LINK}/pkg/shutdown@latest; \
GOPROXY=direct go get ${REPO_LINK}/pkg/logger@latest; \
GOPROXY=direct go get ${REPO_LINK}/pkg/database@latest
endef

.PHONY: test-iam test-iam-coverage update-all-packages

test-iam:
	go test -v ./services/iam/...

test-iam-coverage:
	go test -v -coverprofile=services/iam/coverage.out ./services/iam/...

iam-db:
	@docker-compose up -d iam_database

update-shared-packages:
	@for svc in $(SERVICES); do \
		echo "==> $$svc"; \
		( \
			cd $$svc && \
			$(update_pkg) \
		) || exit $$?; \
	done

update-shared-packages-deps:
	@for svc in $(SHARED_PACKAGES); do \
		echo "==> $$svc"; \
		( \
			cd $$svc && \
			go get -u ./... && \
			go mod tidy \
		) || exit $$?; \
	done
