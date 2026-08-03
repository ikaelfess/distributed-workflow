# Distributed Workflow / Job Orchestration Platform

This project simulates a production-grade distributed workflow orchestration platform similar to CI/CD systems or data pipelines. The platform allows users to define workflows consisting of multiple tasks, execute them across distributed workers, store execution metadata, and receive notifications about results.

The system is intentionally designed to practice real-world backend engineering concepts:

- Go microservices
- gRPC and HTTP APIs
- Kafka event-driven architecture
- Caddy API gateway with Authelia forward-auth
- MongoDB for flexible job metadata
- PostgreSQL for relational services

## Project Structure

```bash
distributed-workflow/
├── infra/
│   └── caddy/               # Caddy API gateway configuration
│
├── pkg/                     # Shared packages
│
├── services/
│   ├── artifact/            # Artifact storage service
│   ├── auth/                # Authelia (human sign-in packaging)
│   ├── metadata/            # Metadata storage service
│   ├── notification/        # Notification service
│   ├── scheduler/           # Workflow/job scheduler service
│   ├── worker/              # Distributed worker service
│   └── workflow/            # Workflow definition and management service
│
├── Makefile                 # Build automation and development commands
├── README.md                # Project documentation
└── compose.yml              # Docker Compose configuration
```

## Services Overview

| Service              | Primary Protocol | Database     | Purpose                                      |
|----------------------|------------------|--------------|----------------------------------------------|
| Auth (Authelia)      | HTTP             | PostgreSQL   | Human browser sign-in (infra, not a domain)  |
| Workflow Service     | HTTP             | PostgreSQL   | Workflow definitions and execution requests  |
| Scheduler Service    | gRPC             | PostgreSQL   | Task orchestration and dependency resolution |
| Worker Service       | gRPC             | None/Local   | Distributed job execution                    |
| Job Metadata Service | HTTP             | MongoDB      | Store logs and runtime metadata              |
| Notification Service | HTTP             | PostgreSQL   | User notifications                           |
| Artifact Service     | HTTP             | Object Store | Store workflow outputs                       |

---

## Auth (Authelia)

Infrastructure packaging under `services/auth/`. Proves a human is logged in via the Authelia portal; Caddy forward-auth gates requests. Users are created out-of-band (file/CLI), not via public signup. Resource permissions stay in owning domain services.

Local smoke steps: [services/auth/TESTING.md](./services/auth/TESTING.md).

Implementation Tasks

- Authelia configuration and file-based users for local development
- Postgres storage for Authelia state (sessions, etc.)
- Caddy `forward_auth` against Authelia
- Wire protected routes for domain services through Caddy
- Document how to add users out-of-band

---

## Workflow Service

Manages workflow definitions (DAGs) and user-triggered executions.

Implementation Tasks

- Create CRUD HTTP endpoints for workflows
- Design database schema for workflows and tasks
- Implement DAG validation logic
- Publish `workflow.started` events to Kafka
- Assume requests are already authenticated at the gateway
- Implement workflow versioning
- Add pagination and filtering for workflow queries

---

## Scheduler Service

Listens to workflow events and determines which tasks should run next.

Implementation Tasks

- Consume Kafka topic `workflow.started`
- Resolve DAG dependencies
- Schedule tasks in correct order
- Publish `task.scheduled` events
- Implement retry and backoff logic
- Maintain task state tracking
- Provide gRPC interface for querying task state

---

## Worker Service

Executes tasks and reports results.

Implementation Tasks

- Subscribe to `task.scheduled` Kafka topic
- Execute tasks using pluggable executors
- Report results via Kafka events
- Publish `task.completed` and `task.failed`
- Implement heartbeat mechanism
- Add concurrency control for task execution

---

## Job Metadata Service (MongoDB)

Stores dynamic job execution metadata and logs.

Implementation Tasks

- Design MongoDB document schema for job execution
- Implement HTTP endpoint to retrieve job details
- Store task logs and execution metrics
- Consume Kafka events `task.completed` and `task.failed`
- Implement search for job history
- Add log streaming endpoint

---

## Notification Service

Handles sending alerts when workflows complete or fail.

Implementation Tasks

- Consume Kafka `workflow.completed` events
- Implement email and webhook notifications
- Create subscription management API
- Add retry logic for failed notifications
- Store notification history

---

## Artifact Service

Stores files produced by workflow tasks.

Implementation Tasks

- Integrate object storage (S3 or MinIO)
- Implement file upload/download endpoints
- Associate artifacts with workflow runs
- Add metadata storage for artifacts
- Generate pre-signed download URLs

---

Infrastructure Tasks

- Deploy Kafka and create required topics
- Configure Caddy as API gateway with Authelia forward-auth
- Add routing rules for each service
- Create Dockerfiles for all services
- Provide docker-compose setup for local development
- Add centralized logging
- Add distributed tracing with OpenTelemetry
- Add Prometheus metrics
