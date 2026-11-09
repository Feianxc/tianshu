# Security policy

Tianshu holds an inventory of your infrastructure and gives AI agents read access to it, so security reports are welcome and taken seriously.

## Reporting a vulnerability

Please **do not open a public issue**. Use GitHub's private vulnerability reporting instead: *Security* tab → *Report a vulnerability*. Include the version or commit, the steps to reproduce, and what an attacker could gain.

You should get a first answer within a week. Fixes are released as soon as they are ready, and reporters are credited unless they prefer otherwise.

## Scope

In scope: the hub (`server/`), the collector, the web console, the MCP and OAuth endpoints, the deployment files and the Android shell.

Out of scope: misconfigured deployments (for example a console exposed without an identity-aware proxy), denial of service through volume, and issues in third-party services.

## Design notes

The security model is described in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#security-model). In short: identity comes only from the credential, workspaces are isolated in the storage layer, agents are read-only, no secrets are stored, and every agent call is audited.
