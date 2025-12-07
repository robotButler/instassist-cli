# Repository Guidelines

## Project Structure & Module Organization
- Core entrypoint lives in `main.go` (flag parsing) with supporting files: `ui.go` (Bubble Tea model), `noninteractive.go` (CLI flow), and `prompt.go` (schema lookup, parsing); Go module is `instassist` (Go 1.24.x).
- `Makefile` contains build/install/test/run targets; `options.schema.json` defines the AI response schema consumed at runtime.
- Build artifacts land in repo root as `insta`; docs and metadata live in `README.md`, `CHANGELOG.md`, `CLAUDE.md`, and `LICENSE`.
- At runtime the schema is resolved in order: binary directory → current working directory → `/usr/local/share/insta-assist/options.schema.json`.

## Build, Test, and Development Commands
- `make build` — compile the `insta` binary with version ldflags.
- `make run` — build then launch the TUI from the repo (uses alt screen).
- `make test` — build and run smoke checks for `-version` and `-h`; extend with `go test ./...` when you add unit tests.
- `make install` — build, copy binary to `/usr/local/bin`, and install schema to `/usr/local/share/insta-assist` (uses `sudo`); `make uninstall` reverses it.
- Manual build: `go build -o insta .` if you need a quick local binary.

## Coding Style & Naming Conventions
- Follow standard Go formatting: `gofmt -w` and `go vet ./...` before pushing; keep imports sorted.
- Favor concise, imperative naming (`runNonInteractive`, `optionsSchemaPath`), and keep UI state within the `model` struct unless splitting into packages.
- Flags stay kebab-case (`-cli`, `-prompt`, `-output`) with lowercase defaults; maintain consistent help text.
- Keep prompts and schema changes co-located and documented when adjusting option parsing.

## Testing Guidelines
- Place tests in `_test.go` files; prefer table-driven cases for prompt building, schema path resolution, and option selection logic.
- Target coverage on non-interactive execution modes (clipboard/stdout/exec) and parsing of CLI output; mock shell execution where side effects matter.
- Run `go test ./...` for unit coverage and `make test` to ensure smoke checks still pass.

## Commit & Pull Request Guidelines
- Commit messages in history are short and imperative (e.g., `Improve TUI: compact layout`, `Fix paste handling`); keep that style.
- Scope commits tightly; include a brief body when behavior changes or new flags are introduced.
- PRs should state user-facing impact, list test evidence (`go test ./...`, manual TUI steps), and call out schema or install-path changes; add screenshots or terminal captures for UI tweaks.

## Security & Configuration Tips
- Ensure required AI CLIs (`codex`, `claude`, `gemini`, `opencode`) are on `PATH`; avoid invoking untrusted binaries in `-output exec` flows.
- Keep `options.schema.json` synchronized with code changes; document schema updates in PRs so downstream installs stay consistent.
- Avoid putting secrets in prompts—the TUI echoes input and does not mask fields.
