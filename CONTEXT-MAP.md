# Context Map

This map is the repository-level navigation for domain documentation. It records service areas without assuming that every service is already a bounded context.

Identity and Access is a **confirmed bounded context**; all other service areas remain **provisional**. Promote a service area only after its distinct domain language and ownership boundary have been explicitly resolved. Code or independent deployment alone is not sufficient.

## Service areas

- **[Identity and Access](./services/iam/CONTEXT.md)** (`services/iam/`) — confirmed. Human and service identity, authentication proof, and platform-wide User Access Levels.
- **Workflow Definition** (`services/workflow/`) — provisional. Workflow definitions and execution requests. Intended glossary: `services/workflow/CONTEXT.md`.
- **Scheduling** (`services/scheduler/`) — provisional. Task orchestration and dependency resolution. Intended glossary: `services/scheduler/CONTEXT.md`.
- **Task Execution** (`services/worker/`) — provisional. Distributed task execution and result reporting. Intended glossary: `services/worker/CONTEXT.md`.
- **Execution Metadata** (`services/metadata/`) — provisional. Runtime metadata, logs, metrics, and execution history. Intended glossary: `services/metadata/CONTEXT.md`.
- **Notifications** (`services/notification/`) — provisional. User subscriptions and delivery of workflow-result notifications. Intended glossary: `services/notification/CONTEXT.md`.
- **Artifact Storage** (`services/artifact/`) — provisional. Storage and retrieval of outputs produced by workflow tasks. Intended glossary: `services/artifact/CONTEXT.md`.

The intended glossary paths are navigation targets, not required files. Create each file only when domain language for that area has been resolved.

## Shared technical modules

These modules support services but are not bounded contexts and do not receive domain glossaries:

- `pkg/database/`
- `pkg/httpserver/`
- `pkg/logger/`
- `pkg/shutdown/`

## Planned relationships

The following relationships come from the README and are **planned, not authoritative contracts**:

- **Other service areas → Identity and Access**: planned token validation through the `ValidateToken` gRPC operation.
- **Workflow Definition → Scheduling**: planned Kafka event `workflow.started`.
- **Scheduling → Task Execution**: planned Kafka event `task.scheduled`.
- **Task Execution → Execution Metadata**: planned Kafka events `task.completed` and `task.failed`.
- **Unresolved producer → Notifications**: planned Kafka event `workflow.completed`.
- **Envoy → Identity and Access**: planned external authorization for gateway requests.

No Kafka schemas or gRPC protocol definitions currently establish these relationships. Ownership and location of future cross-service contracts are unresolved; do not infer either from this map.
