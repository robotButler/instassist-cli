# Repository Guidelines

## Project Structure & Module Organization
- Core entrypoint in `app.go` (flag parsing, mode selection). Supporting files: `ui.go` (Bubble Tea model/render/shortcuts), `noninteractive.go` (CLI flow), `prompt.go` (prompt construction, schema resolution, JSON parsing). Go module is `instassist` (Go 1.24.x); binary entry lives at `cmd/inst/main.go`.
- `options.schema.json` is required at runtime and is located via executable dir → CWD → `/usr/local/share/insta-assist/options.schema.json`.
- Build artifacts land in repo root as `inst`; docs: `README.md`, `CHANGELOG.md`, `CLAUDE.md`, `AGENTS.md`, `LICENSE`. Automation lives in `Makefile`.

## Build, Test, and Development Commands
- `make build` — compile the `inst` binary with version ldflags.
- `make run` — build then launch the TUI from the repo (uses alt screen).
- `make test` — build and run smoke checks for `-version` and `-h`; use `go test ./...` for unit coverage (prompt parsing, schema lookup, exec flows).
- `make install` — build, copy binary to `/usr/local/bin`, and install schema to `/usr/local/share/insta-assist` (uses `sudo`); `make uninstall` reverses it.
- `make go-install` — install via `go install ./cmd/inst` into `GO_INSTALL_DIR` (GOBIN if set, otherwise GOPATH/bin; override with `GO_INSTALL_DIR=/path`).
- Manual build: `go build -o inst ./cmd/inst` if you need a quick local binary.
- CI: `.github/workflows/ci.yml` runs gofmt (diff check), build, and `go test ./...` on pushes/PRs.

## Coding Style & Naming Conventions
- Standard Go tooling: `gofmt`, `go vet` before pushing; keep imports grouped. Stick to Bubble Tea idioms (model/update/view) and keep UI state in `model` unless you break out packages.
- Flags are kebab-case (`-cli`, `-prompt`, `-output`) with lowercase defaults; update help strings when adding flags.
- New flag: `-stay-open-exec` keeps the TUI open after Ctrl+R and shows command stdout/stderr; default exits after exec.
- Keep prompt format and schema in sync; changes to `buildPrompt` or `options.schema.json` should be documented in PRs.
- Styling is Lipgloss-based; prefer shared styles if expanding the UI.

## Testing Guidelines
- Place tests in `_test.go` files; table-drive prompt building, schema path resolution, option parsing/sorting, and exec path decisions.
- Target non-interactive modes (clipboard/stdout/exec) and Bubble Tea state transitions where practical; mock shell execution to avoid side effects.
- Run `go test ./...` locally; `make test` still provides smoke checks for `-h`/`-version`.

## Commit & Pull Request Guidelines
- Commit messages in history are short and imperative (e.g., `Improve TUI: compact layout`, `Fix paste handling`); keep that style.
- Scope commits tightly; include a brief body when behavior changes or new flags are introduced.
- PRs should state user-facing impact, list test evidence (`go test ./...`, manual TUI steps), and call out schema or install-path changes; add screenshots or terminal captures for UI tweaks.

## Security & Configuration Tips
- Ensure required AI CLIs (`codex`, `claude`, `gemini`, `opencode`) are on `PATH`; avoid invoking untrusted binaries in `-output exec` flows.
- Keep `options.schema.json` synchronized with code changes; document schema updates in PRs so downstream installs stay consistent.
- Avoid secrets in prompts—the TUI echoes input and does not mask fields.

## Interaction Model & Shortcuts
- Input submission: `Enter` sends; `Ctrl+R` sends and auto-executes first result; `Ctrl+Y` toggles YOLO/auto-approve. Newline insertion: `Alt+Enter` or `Ctrl+J`. CLI switching: `Ctrl+N` / `Ctrl+P`. Exit: `Ctrl+C`/`Esc`.
- Viewing mode: navigation with arrows or `j/k`; `Enter` copies and exits; `Ctrl+R` executes selection; `a` starts a refine/append prompt on the same session; `n` starts a new prompt; `Ctrl+Y` toggles YOLO; `Ctrl+N`/`Ctrl+P` switch CLIs. CLI tabs, YOLO toggle, and options are clickable.
- Exec failures surface in the status bar; auto-exec resets after firing to prevent repeated runs.

## Compatibility Notes
- Tested Go toolchain target is 1.24.x; dependencies are pinned to released versions compatible with macOS and common Linux distros.
- Clipboard defaults to xclip/xsel on Linux; on headless systems prefer `-output stdout` to avoid dependency errors.
- Schema is required even for CLIs that accept positional prompts; the binary embeds `options.schema.json` and will write a temp copy if none is present on disk. If temp creation fails, place the schema next to the binary or install via `make install` for `/usr/local/share/insta-assist/`.
- Claude CLI expects the schema JSON string (not a file path) plus `--print --output-format json --json-schema "<json>"`. The app loads the schema contents (embedded or on-disk) and passes the JSON string; keep the schema valid JSON.
- Quick manual verification: use tmux (or another terminal) to run `claude -p "check" --print --output-format json --json-schema "$(cat options.schema.json)"` and confirm structured output; do the same for `codex`/`gemini`/`opencode` with their documented flags.
