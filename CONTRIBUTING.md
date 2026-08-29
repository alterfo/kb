# Contributing to kb

kb follows the repository's AGENTS.md architecture map and the task plans in
docs/plans. Before opening a pull request, run make check locally:

```sh
make check
```

That command runs gofmt, go vet, and go test -timeout 120s ./... with no
network. Live LLM and external integrations are exercised only behind the
integration build tag and KB_LLM_IT=1:

```sh
KB_LLM_IT=1 go test -tags integration ./...
```

Conventions:

- Go only for new code; do not add Python.
- Every code change ships tests in the same change.
- Use the existing dependency-injection seams (EnvLookup, httptest.Server,
  HTTPDoer, in-memory Sink, fake LLM/Embedder/Reranker/GraphStore).
- Keep golden fixtures in testdata/ and use stdlib table-driven tests.
- Do not add code comments unless the change specifically requires one.
- Preserve fail-open behavior: every non-embedding-dimension stage returns a
  partial result plus an error rather than panicking.
- Store secret values only in the environment; sources.yaml contains env-var
  names, never values.

Please open an issue before large changes so the intended design can be
aligned with docs/architecture-review.md.
