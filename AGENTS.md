## Agent skills

### Required skills

- **Go work**: For Go coding, review, debugging, or setup, load `/golang-how-to` first and follow its routing to the relevant specialized Go skills and Go-specific sources.
- **Current documentation**: For non-Go libraries, frameworks, SDKs, APIs, and CLIs, load `/find-docs` and verify details with Context7. For Go packages, prefer the `gopls` or `godig` route selected by `/golang-how-to`; use `/find-docs` only as a fallback.

### Issue tracker

Issues and PRDs are tracked in GitHub Issues. See `docs/agents/issue-tracker.md`.

### Delivery

Solving-issue work lands via a dedicated branch and a PR linked with `Fixes #<n>` / `Closes #<n>` — never directly on `main`. See `docs/agents/delivery.md`.

### Triage labels

The default canonical triage labels are used. See `docs/agents/triage-labels.md`.

### Domain docs

This repository uses a provisional multi-context map with lazy, service-local glossaries. See `docs/agents/domain.md`.
