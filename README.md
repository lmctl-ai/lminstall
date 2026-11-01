# lminstall

A single static Go application to install, verify, upgrade and uninstall coding
provider CLIs using their official distribution channels. Linux amd64/arm64,
macOS amd64/arm64 and Windows are supported by the application; provider-specific
availability is verified by the live Actions matrix below.

```sh
CGO_ENABLED=0 go build -trimpath -o lminstall ./cmd/lminstall
./lminstall list
./lminstall install claude codex
./lminstall status --json
./lminstall upgrade codex
./lminstall uninstall codex
```

`--root DIR` or `LMINSTALL_ROOT` chooses the owned prefix. Defaults:
`$XDG_DATA_HOME/lminstall` (otherwise `~/.local/share/lminstall`) on Linux/macOS,
and `%LOCALAPPDATA%\lminstall` on Windows. Each provider has its own prefix.
Install output reports `bin_dir`; add that directory to PATH. In GitHub Actions
successful installs append it to GITHUB_PATH automatically. No shell profile is
edited by lminstall. Existing external installs, credentials, sessions and config
are not adopted or uninstalled. Upstream script bootstraps use an owned HOME.

Install is idempotent only after the managed binary passes `--version`; a broken
or partial install is repaired on the next install. Upgrade asks the same official
channel for its newest version, then verifies it. Stop a provider before upgrading
on Windows or when its own package manager requires it. Uninstall removes only the
provider's owned directory, not shared runtime caches or external installations.
Release status checks both its payload and stable PATH launcher; a missing or
changed launcher is broken and the next install repairs it.
On modern Windows, owned files use POSIX-style namespace deletion so cached image
sections can be released without a delay loop. A running executable can still
prevent deletion: stop that provider and repeat uninstall; the ownership marker
is retained so retry is safe. No unrelated process is stopped.
Unowned directories are refused. Concurrent operations on one provider fail busy
immediately; no polling or waiting policy is added. If an installer is killed,
remove its named lock only after confirming that installer process has exited.

No application timeouts, sleeps, output caps, model prompts or authentication.
Child output streams to stderr; JSON remains on stdout. Verification requires a
successful `--version` with a version number. Release archives and updater
artifacts must also report the selected tag/manifest version as a complete version
token (a leading `v` and surrounding banner text are accepted). Install failures are errors, never
reported as installed. `status` reports absent/incomplete/broken/installed or
unavailable; its query exit is 0. Exit 1 is an operation failure, 2 invalid CLI/data,
3 a declared unavailable distribution. A real failure dominates unavailable rows.

## Providers and official channels

Inventory follows lmctl's native adapter registry, including the built-in mock
fixture. The manifest is [providers.json](internal/install/providers.json).
`list --json` enumerates it; available means a recipe exists, not a claimed live
model certification. No API keys are needed to install or verify versions. GitHub release lookup falls
back from API quota errors to public release redirects and the publisher's
checksum asset; it does not ask for tokens or wait on a retry timer. A successful
API response with a missing/null/empty asset digest uses the declared checksum
asset from that same release. Missing, malformed, conflicting or incorrect
checksums fail before executing the artifact; releases never skip integrity checks.
All metadata, script and artifact requests require HTTPS through every redirect.

| ID | Official channel / prerequisite |
| --- | --- |
| [claude](https://code.claude.com/docs/en/setup) | npm @anthropic-ai/claude-code; Node/npm |
| [codex](https://github.com/openai/codex) | npm @openai/codex; Node/npm |
| [gemini](https://github.com/google-gemini/gemini-cli) | npm @google/gemini-cli; Node/npm |
| [copilot](https://docs.github.com/en/copilot/how-tos/copilot-cli/install-copilot-cli) | npm @github/copilot; Node/npm |
| [opencode](https://opencode.ai/docs/) | npm opencode-ai; Node/npm |
| [kimi](https://moonshotai.github.io/kimi-code/en/guides/getting-started) | npm @moonshot-ai/kimi-code (current successor to archived kimi-cli); Node/npm |
| [qwen](https://github.com/QwenLM/qwen-code) | npm @qwen-code/qwen-code; Node/npm |
| [pi](https://github.com/earendil-works/pi) | npm @earendil-works/pi-coding-agent, official --ignore-scripts; Node/npm |
| [dsh](https://deepseekdocs.com/en/docs/user-guide/cli) | npm @deepseek-ai/dsh; Node/npm |
| [miniplayer](https://github.com/lmctl-ai/miniplayer) | npm from its official public Git source; Node/npm and Git (no published releases at initial inspection) |
| [lmplayer](https://github.com/lmctl-ai/lmplayer/releases) | Official release archive; SHA-256 from release metadata or official SHA256SUMS; no Node runtime |
| [hermes](https://github.com/NousResearch/hermes-agent) | official Git project and frozen uv sync, Python 3.14 core dependencies; uv and Git |
| [agy](https://antigravity.google/download) | Official updater manifest and SHA-512 artifact, without its system setup handoff |
| [muse](https://dev.meta.ai/lp/muse-code) | Official shell/PowerShell installer and launcher, owned directory; no login during install |
| claudemock | Unavailable as a standalone CLI: it is an lmctl fixture. Enable LMCTL_ENABLE_MOCK_PROVIDER=1 in lmctl. No fake installation/version is substituted. |

The application is static, not the providers' runtimes. Node 24/npm and uv are
provisioned by the composite Action. A standalone user must supply the prerequisite
for their chosen channel; missing commands fail with an actionable prerequisite
message. Providers may require Git Bash on Windows to operate beyond --version.
Hermes uses upstream's core package; optional integrations/extras remain upstream
package-management choices. Installation does not configure a model or API key.

## Reusable composite Action

```yaml
steps:
  - uses: lmctl-ai/lminstall@main # pin a reviewed commit in production
    with:
      providers: claude codex kimi
  - shell: bash
    run: |
      claude --version
      codex --version
      lminstall status --json
```

Inputs: `providers` (required space-separated IDs), optional `root`.
Output: `executable`, the native lminstall path. It builds this repository with
CGO_ENABLED=0, sets up Node/npm and uv, installs the requested providers and exports
paths and LMINSTALL_ROOT so later status/upgrade/uninstall commands use the same
prefix. No token/key inputs, release fetching of lminstall itself or manual tags.
It fails when a requested provider is unavailable; it never silently skips one.

## CI evidence and exceptions

The `ci` workflow runs the full catalog on Ubuntu amd64/arm64, macOS amd64/arm64,
and Windows amd64. It attempts install, repeated install without rewriting its
receipt, status, upgrade on the same bin_dir, uninstall and absent status. It keeps
all provider results even after a failure. Separate Ubuntu/macOS/Windows jobs use
this repository's composite Action as a consumer. Artifacts and job summaries
record versions, failures and declared unavailable reasons.

[Actions 41313617951](https://github.com/lmctl-ai/lminstall/actions/runs/41313617951)
passed on code revision 115341a: **14 native providers verified on all five targets**
(70 successful install/repeat/status/upgrade/uninstall lifecycles), plus all three
composite consumer jobs. ClaudeMock is the only declared exception: it is built
into lmctl and has no standalone distribution. No native provider required an
unattended-install OS exception on these runners. Authentication/model operation
is separate and was not exercised; no live model request is part of the workflow.
Exact versions and upstream failures remain in the per-job artifacts/summaries.

Docs-only changes do not rerun network installers. Go code, catalog data, Action,
workflow and test changes still run the complete live matrix.

## Manifest and development

Adding a provider is data: id, binary, official source link and per-OS recipe.
Generic channels are npm, uv tools, Git projects, checksum update manifests, GitHub release archives and downloaded official
installer steps. Recipes contain argv arrays, owned paths and environment values;
root paths never interpolate into shell code. Use `--manifest FILE` to explicitly
replace the embedded catalog with another trusted manifest; provider IDs and
per-platform recipes are never inherited. Downloaded installer steps require a
`digest: "sha256:<64 hex characters>"`; a mismatch prevents execution.
Manifests execute installation code and are trusted
configuration; official source/provenance review is part of adding an entry.

The embedded Muse bootstrap scripts have reviewed SHA-256 content pins. An upstream
script change fails closed and requires reviewing and updating its catalog hash;
the bootstrap still chooses its upstream provider payload. Action dependencies
use full commit IDs. These pins do not freeze provider versions: npm `@latest`,
Miniplayer's official Git default branch and Hermes's official Git HEAD remain
intentional newest-version sources for install/upgrade. npm/uv integrity and HTTPS
protect transfers; they do not make mutable upstream publishers reproducible or
protect against a compromised publisher. A full provider lockfile/signature system
would be a separate design, outside these fixes. Pin this Action to a reviewed full
commit ID in consumers; `@main` below is the moving development example.

```sh
go test ./...
go vet ./...
CGO_ENABLED=0 go build -trimpath -o build/lminstall ./cmd/lminstall
```

Offline tests use fake runners, TLS fixture downloads and child test executables.
They cover catalog replacement, exit contracts, idempotency, upgrade/PATH stability,
ownership, missing/null digests, HTTPS downgrade refusal, exact selected versions, checksum failure,
archive traversal and >1 MiB payload/output. Real installs run only in Actions.
Run `python3 scripts/mutate.py` to check production fault controls against the suite.
No releases or tags are published manually.
