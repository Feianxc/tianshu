# Contributing

Thanks for your interest. Issues and pull requests are welcome; for larger changes, please open an issue first so we can agree on the approach.

## Development setup

Requirements: Go 1.27+, Node.js 20+.

```bash
cd web && npm ci && npm run build && cd ..   # the console is embedded into the Go binary
cd server && go vet ./... && go test ./...
```

`npm run dev` in `web/` starts Vite on port 5173 and proxies `/api` and `/mcp` to a hub on `127.0.0.1:8787`. Use `examples/catalog` for data; never commit a real inventory.

## Conventions

- **Evidence over assumptions.** A missing signal is "unknown", never "OK". Keep health, freshness, lifecycle and acceptance separate.
- **Isolation lives in the store.** New read paths take a `model.Viewer` and must be covered by the isolation tests in `server/internal/api`.
- **No secrets.** Do not add fields or tools that store or return passwords, keys or tokens.
- **Tests describe behaviour.** Name them after what the user should observe, as the existing tests do.
- Run `gofmt`, `go vet` and `npm run typecheck` before sending a pull request.

By contributing you agree that your contributions are licensed under the MIT License.
