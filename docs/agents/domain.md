# Domain Docs

How engineering skills should consume and extend this repository's domain documentation.

## Before exploring

1. Read the root `CONTEXT-MAP.md` to identify the relevant service area, its status, and its intended glossary path.
2. Read the relevant `services/<service>/CONTEXT.md` if it exists.
3. Read applicable system-wide ADRs under `docs/adr/` and service-specific ADRs under `services/<service>/docs/adr/`.

Missing glossary or ADR files are intentional until domain language or a durable decision has actually been resolved. Proceed silently rather than creating placeholders.

## Boundary status

The context map distinguishes two states:

- **Provisional service area**: a responsibility and code location exist, but its distinct domain language or ownership boundary has not been resolved.
- **Confirmed bounded context**: its distinct domain language and ownership boundary have been explicitly resolved.

Implementation or independent deployability alone does not promote a service area. Use `/domain-modeling` to resolve the boundary, then update its status in `CONTEXT-MAP.md`.

## Context glossaries

Create glossaries lazily at `services/<service>/CONTEXT.md`. A context glossary contains domain language only: concise definitions, canonical terms, and terms to avoid. It must not contain implementation details, plans, or unresolved guesses.

Do not create a glossary merely because a service directory exists. The current absence of per-service `CONTEXT.md` files is deliberate.

## Shared technical modules

Modules under `pkg/` are shared technical infrastructure, not bounded contexts. They may be listed in the context map for navigation, but they do not receive domain glossaries.

## Relationships and contracts

`CONTEXT-MAP.md` may describe Kafka, gRPC, HTTP, or infrastructure relationships that the README plans. Treat every such relationship as non-authoritative until a schema, protocol definition, or implementation establishes it.

Published contracts are producer-owned and versioned beside the service that defines them, as established by [ADR-0002](../adr/0002-producer-owned-contracts.md). A planned relationship still remains non-authoritative until its producer-owned schema or protocol definition exists.

## Use the glossary's vocabulary

When output names a domain concept—in an issue title, refactor proposal, hypothesis, or test—use the term defined by the relevant context glossary. Do not drift to synonyms it explicitly avoids.

If a needed concept is absent, reconsider whether the codebase already uses another term. If the gap is real, resolve it with `/domain-modeling` before adding it.

## Flag ADR conflicts

If output contradicts an existing ADR, surface the conflict rather than silently overriding it:

> _Contradicts ADR-0007 (event-sourced orders) — but worth reopening because…_
