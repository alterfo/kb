# Security Policy

kb is a local, single-user GraphRAG knowledge base. The web dashboard binds
only to loopback addresses (127.0.0.1 by default), has no authentication, and
exposes destructive routes for documents, graph entities, relations, and
integrations. Treat it as a trusted local tool, not a multi-tenant service.

If you expose the dashboard beyond loopback, place it behind an SSH tunnel or
a reverse proxy that provides authentication and authorization. Do not expose
the built-in HTTP listener directly to an untrusted network.

Connector credentials and API keys belong in environment variables, not in
sources.yaml or git. sources.yaml stores only environment-variable names.

## Reporting a vulnerability

Use GitHub's private vulnerability reporting for this repository (the
"Report a vulnerability" button under **Security → Advisories**). Please do
not open public issues for security reports, and do not include live
credentials or private corpus data in the report. If private reporting is
unavailable, email the maintainer listed in the repository metadata.
