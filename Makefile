.PHONY: test-iam test-iam-coverage

test-iam:
	go test -v ./services/iam/...

test-iam-coverage:
	go test -v -coverprofile=services/iam/coverage.out ./services/iam/...

iam-db:
	@docker-compose up -d iam_database
