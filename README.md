# lminstall

A single static Go application to install, verify, upgrade and uninstall coding
provider CLIs using their official distribution channels. **lminstall never shells
out to install anything**: no npm, git, curl, tar, unzip, PowerShell, bash, uv or
vendor installer script is started. Downloads, SHA-256/SHA-512 verification,
archive extraction, npm registry resolution and launcher creation are all Go. The
only child process lminstall starts while installing is the freshly installed
provider's own `--version` (and, for Hermes, the vendor's own pinned PM tools). Linux amd64/arm64,
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
are not adopted or uninstalled. Native bootstrap implementations use an owned HOME. No vendor installer .sh or
.ps1 is downloaded, embedded or executed by the product. Inspected vendor scripts
are translated into Go download/verification/extraction/bootstrap operations.

For npm providers (Gemini, Pi, DSH, Miniplayer) lminstall contains a Go npm client
equivalent to a user-prefix `npm install -g`: registry packument, `latest` dist-tag
and semver range resolution, optional and platform (`os`/`cpu`/`libc`) dependencies,
`npm:` aliases, sha512 integrity-checked tarballs, hoisting, and bin links
(`bin/` symlinks, `.cmd` shims on Windows). Lifecycle scripts are never executed
(as with `--ignore-scripts`). These packages are JavaScript, so running them (and
the `--version` check) needs a `node` on PATH; npm itself is not needed.
Claude Code, Codex, Copilot, OpenCode and Qwen use the vendors' native binary
channels (below), which need no Node. Native
Kimi, Antigravity and Muse do not need Node/npm/uv. Hermes bootstraps its own pinned
uv, Python and PM-managed Node/runtime tools, even when none is on PATH.

Existing Kimi/Muse/Hermes receipts from older methods are marked incomplete
until `install` migrates them once. A receipt records its channel before later
installs reuse it; an old script or npm command cannot masquerade as the native
method. Refresh PATH from the returned bin_dir after a method change.

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

Installation has no application timeouts, sleeps, output caps, model prompts or authentication.
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
| [claude](https://code.claude.com/docs/en/setup) | Native installer flow in Go: `downloads.claude.ai` latest version, per-version `manifest.json` platform SHA-256 and size, versioned payload + PATH launcher; no Node |
| [codex](https://github.com/openai/codex) | GitHub release archive for the platform triple (`rust-v*` tags), asset SHA-256 digest; no Node |
| [gemini](https://github.com/google-gemini/gemini-cli) | npm @google/gemini-cli through the Go npm client; runs on Node |
| [copilot](https://docs.github.com/en/copilot/how-tos/copilot-cli/install-copilot-cli) | The vendor install script's flow: `copilot-<os>-<arch>` release archive verified against the release `SHA256SUMS.txt`; no Node |
| [opencode](https://opencode.ai/docs/) | The vendor install script's flow: `opencode-<os>-<arch>` release archive from GitHub; no Node |
| [kimi](https://github.com/MoonshotAI/kimi-code) | Native latest/platform manifest and SHA-256 binary; region marker; no Node runtime |
| [qwen](https://github.com/QwenLM/qwen-code) | Vendor standalone archive (bundles its own Node), verified against release `SHA256SUMS`; no host Node |
| [pi](https://github.com/earendil-works/pi) | npm @earendil-works/pi-coding-agent through the Go npm client (scripts never run); runs on Node |
| [dsh](https://github.com/deepseek-ai/deepseek-harness) | npm @deepseek-ai/dsh through the Go npm client; runs on Node |
| [miniplayer](https://github.com/lmctl-ai/miniplayer) | Its GitHub source tarball installed by the Go npm client (no git executable); runs on Node |
| [lmplayer](https://github.com/lmctl-ai/lmplayer/releases) | Official release archive; SHA-256 from release metadata or official SHA256SUMS; no Node runtime |
| [hermes](https://hermes-agent.nousresearch.com/docs/getting-started/installation) | Latest stable tag cloned in-process (pure-Go git, depth 1; no git executable); checksum-pinned uv, Python 3.14 and the vendor's pinned PM install run from the owned prefix |
| [agy](https://antigravity.google/download) | Official updater manifest and SHA-512 artifact, without its system setup handoff |
| [muse](https://dev.meta.ai/lp/muse-code) | Native muse-stable channel/release manifest, SHA-256 and exact artifact size; restricted downloads fail without authentication |
| claudemock | Unavailable as a standalone CLI: it is an lmctl fixture. Enable LMCTL_ENABLE_MOCK_PROVIDER=1 in lmctl. No fake installation/version is substituted. |

Gemini CLI's current vendor guide still supports npm installation, but describes
it as an enterprise Code Assist workflow. Free individual/Google AI plan access
moved to Antigravity; installation and --version do not certify account/model
access. The two commands remain separate provider entries.

The application is static, not the providers' runtimes. Node 24/npm are
provisioned by the composite Action (Node/npm only; Hermes owns uv). A standalone user must supply the prerequisite
for their chosen channel; missing commands fail with an actionable prerequisite
message. Providers may require Git Bash on Windows to operate beyond --version.
Hermes follows upstream's noninteractive source installer: the latest stable
checkout, verified tool pins from pm/lock.json, PM's default dependencies and
source product completion. Its browser/computer-use tools follow upstream's
default policy. Interactive setup, model login and gateway/service setup are left
to the user. POSIX Git/Xcode tools, native compilers and system libraries such as
Linux libatomic/Chromium libraries may require an administrator. lminstall never
runs sudo or a system package manager: its POSIX PM environment excludes those
commands. Windows can bootstrap the official checksum-pinned portable Git.
When a dependency must build from source (for example cryptography on Intel
macOS), provide its native compiler/Rust prerequisites. The existing RUSTUP_HOME
(or caller ~/.rustup) remains available despite private HOME; Cargo downloads and
build caches use the owned prefix. lminstall does not install a system toolchain.
No user shell profiles or system services are changed; native Antigravity's
PATH/service setup handoff and Kimi's migration of external legacy shims are
intentionally omitted to respect owned-prefix boundaries. Installation does not configure a model or API key.

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

Inputs: `providers` (required space-separated IDs), optional `root`. An ambient
`GITHUB_TOKEN`/`GH_TOKEN` is used only to raise the GitHub release-API quota (as the
vendors' own scripts do); it is never sent to downloads.
Output: `executable`, the native lminstall path. It builds this repository with
CGO_ENABLED=0, sets up Node/npm, installs the requested providers and exports
paths and LMINSTALL_ROOT so later status/upgrade/uninstall commands use the same
prefix. No token/key inputs, release fetching of lminstall itself or manual tags.
It fails when a requested provider is unavailable; it never silently skips one.

## Examples

[`examples/`](examples/) has runnable scripts (install a provider and test it with the right and a
wrong API key against lmmock, `lminstall env` for orchestrators) and a consumer GitHub Actions
workflow. See [examples/README.md](examples/README.md).

## Releases and CI

`release.yml` runs on every push to `main`. It calls `ci.yml` (unit tests plus real
installs of every provider on Linux amd64/arm64, macOS amd64/arm64 and Windows
amd64, with **no npm, git, uv, curl or tar on PATH**; JavaScript providers get a
PATH containing only `node`) and `launch-mock.yml` (every installed provider driven
against the released lmmock API mock). Only if both pass does it cross-compile static
binaries for linux/darwin/windows × amd64/arm64 and publish them with `SHA256SUMS`
as a GitHub Release (`v0.1.N`). Nothing is published to npm.

`oauth-mock.yml` installs Codex with lminstall and performs a real `codex login --device-auth` (plus refresh, logout and failure modes) against lmmock's mocked OAuth server: lminspect terminates TLS for `auth.openai.com` and routes it to lmmock. No account or network login is involved.

`launch-mock.yml` uses `lmmock --builtin llm-api` (no Node or script compiler). On
Ubuntu the real client talks to [lminspect](https://github.com/lmctl-ai/lminspect)
in reverse-proxy mode in front of lmmock; the recorded `flows.jsonl` and the
`coverage-gaps.json` report list every request a CLI made that lmmock did not
serve, which is how new routes for lmmock are discovered.

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

Npm/release entries are data: id, binary, official source link and per-OS recipe.
A new native installer protocol needs Go implementation and regression tests.
Channels include npm, Git projects, checksum update manifests, GitHub release
archives and Go-native Kimi/Muse/Hermes installers. Recipes contain argv arrays, owned paths and environment values;
root paths never interpolate into shell code. Use `--manifest FILE` to explicitly
replace the embedded catalog with another trusted manifest; provider IDs and
per-platform recipes are never inherited. Downloaded installer steps are refused; provider-specific installer logic lives
in Go, and release payloads retain their vendor checksum verification.
Manifests execute installation code and are trusted
configuration; official source/provenance review is part of adding an entry.

Kimi follows the vendor's compressed-tarball-first order, then its bare-binary
fallback on download/extraction failure. The extracted binary must match the
versioned manifest's SHA-256; a wrong binary hash never falls back. The container
has no separate publisher hash, and only the verified native binary is published. Muse's script checks its
launcher against an optional HTTP hash header, then its launcher requires a
SHA-256 and exact size for the native payload; Go skips the launcher entirely
and enforces both payload checks. Antigravity uses the official updater SHA-512.
Hermes's uv and Windows Git bootstrap archives have the upstream SHA-256 pins;
uv can use the vendor's content-addressed mirror on availability failures, while
checksum/TLS/policy failures remain errors;
its PM verifies tool hashes and the uv.lock dependency graph. No installer-script
hash mechanism remains. HTTPS is required for requests and redirect hops.

Official supported npm methods are retained for Claude (the setup page supports
npm although the repository recommends native installation), Codex, Gemini,
Copilot, OpenCode, Qwen, Pi (`--ignore-scripts`) and DSH. Qwen/Pi/Codex/OpenCode
also advertise standalone script methods; these remain documented vendor
alternatives rather than shell execution paths. Miniplayer's README advertises
standalone releases but the latest-release API currently returns 404; its official
source/npm method remains selected. lmplayer uses its own official release
archives, never OpenCode's installer or package names.

Action dependencies use full commit IDs. These pins do not freeze provider
versions: npm `@latest` and Miniplayer's Git default branch intentionally follow
new releases. Publisher compromise defenses and reproducible transitive locks
remain upstream trust concerns. Pin this Action to a reviewed full commit ID.

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

## Configure and run a provider

Installation and model routing are separate operations. The launch commands use
version 1 `launch_config` entries in the same provider catalog. They do not execute
vendor installer scripts, call Ollama launch, or modify your existing tool config.

```sh
lminstall configure claude --backend mock --base-url http://127.0.0.1:8080 \
  --model mock-agent --credential-env MOCK_API_KEY --profile-dir /absolute/profile
lminstall run claude --profile-dir /absolute/profile -- -p 'Hello' --tools ''
lminstall env claude --profile-dir /absolute/profile --runtime-dir /absolute/runtime --format github-env
```

`configure` emits a JSON plan by default (`--format plan`). It writes `profile.json`
and nonsecret config previews under an owned, absolute `--profile-dir`; nonempty
unowned directories and profiles owned by another provider are refused. It never
reads the named credential environment variable. `--format github-env` emits only
LMINSTALL_PROFILE_DIR, LMINSTALL_PROVIDER, LMINSTALL_BACKEND and LMINSTALL_MODEL.
It does not emit provider credential bindings or shell code to evaluate.
`--root`/LMINSTALL_ROOT selects the installed prefix at configure time, and the
profile remembers it. Reconfigure a profile to pick up changed catalog routing.
Custom manifests are trusted executable configuration, including launch adapters.

`run` starts the managed binary with literal arguments after `--`. Each invocation
gets a new HOME, USERPROFILE, XDG directories and working directory. These runtime
files are removed on exit; sessions and work produced there are ephemeral. It does
not reuse your login or ambient API-key environment. This isolation is configuration
isolation, not an OS sandbox; provider tools still run with your user's privileges.
Required model/routing switches precede your arguments; avoid overriding them.
The provider's exit code is preserved; invalid profile/CLI data exits 2, launch or
missing runtime credential failures exit 1. Stdout and stderr remain separate.

`env` is for orchestrators that start the installed provider themselves, many
times, with shared state (lmctl seeds a session and later resumes it; `run` would
give every invocation a fresh, discarded HOME). It renders a mock-backend profile
into one persistent, absolute `--runtime-dir` (HOME, config files such as Codex's
`config.toml`, a work directory) and prints what to export: a JSON plan by
default, `--format github-env` KEY=VALUE lines for `$GITHUB_ENV`, or `--format sh`
POSIX `export` lines. Only the mock backend is accepted (`env_requires_mock_backend`
otherwise): no credential is read or bound, the literal loopback base URL and the
nonsecret `lminstall-mock-key` placeholder are what the provider sees. Rendering
again into the same directory is idempotent and keeps the state already there.
The exported lines also carry `LMINSTALL_RUNTIME_DIR`, `LMINSTALL_EXECUTABLE`
(the installed binary) and the profile's provider, backend, model and base URL.

```sh
lminstall env claude --profile-dir /absolute/profile --runtime-dir /absolute/runtime --format sh > claude.env
. ./claude.env && lmctl seed team.lmctl   # lmctl spawns `claude` with that routing
```

Secrets are looked up only at run time from `--credential-env NAME`. Do not put a
key in the model, URL, argv, profile path, manifest or Action inputs. A Go loopback
bridge binds authorization to the configured upstream in memory; the child sees
only a fresh random placeholder. This also supports tools that require a key in
a config file without persisting the actual key. No matching key is sent to an
unauthenticated loopback request. Response headers/body and child output redact
raw credentials and common URL/JSON/unicode/hex/base64/UTF-16 encodings. Arbitrary
transforms are not a universal data-loss-prevention guarantee. No request payload,
credential, live response or environment dump is logged by lminstall itself.
There are no application deadlines, sleeps or output caps.

| Backend | Supported tools | Qualification |
| --- | --- | --- |
| mock | claude, codex, gemini, copilot, opencode, kimi, qwen, pi, dsh, miniplayer, lmplayer, hermes, agy | HTTP literal-loopback base; native API dialects against released lmmock |
| ollama-cloud | claude | HTTPS origin https://ollama.com; named credential required; Anthropic Messages compatibility |
| ollama-local | none yet | Explicit unsupported error; no inferred compatibility |

Muse has no qualified launch adapter; claudemock is an lmctl fixture. Other tools
explicitly refuse Ollama Cloud. Unknown dialect qualification belongs to lminspect.
OpenRouter and DeepSeek live routes are not advertised as supported backends.
Gemini/Antigravity need model IDs their clients recognize, even with a mock server.
Claude binds the requested model to primary, default tier and subagent slots.
Credentials with surrounding whitespace or control characters are rejected before
starting a child or bridge; they are never silently trimmed. Provider exit codes
and separate stdout/stderr are passed through without an added failure diagnostic.
Origin-shaped adapters reject a `/v1` suffix; versioned adapters accept an origin
or a single `/v1` and produce one `/v1`. Other paths (including repeated `/v1`)
are rejected before creating or changing a profile. Plans describe these bindings explicitly.

After the installation Action, another repository can use the configure step:

```yaml
- uses: lmctl-ai/lminstall@5054af1f6a10198b362ea51b3502504c17915f65 # reviewed immutable install/configure pair
  with:
    providers: claude
- uses: lmctl-ai/lminstall/configure@5054af1f6a10198b362ea51b3502504c17915f65
  with:
    provider: claude
    backend: ollama-cloud
    base-url: https://ollama.com
    model: glm-5.3-flash
    credential-env: OLLAMA_API_KEY
    profile-dir: ${{ runner.temp }}/claude-profile
- shell: bash
  env:
    OLLAMA_API_KEY: ${{ secrets.OLLAMA_API_KEY }}
  run: lminstall run claude --profile-dir "$LMINSTALL_PROFILE_DIR" -- -p 'Hello' --tools ''
```

For an orchestrator in Actions, `lminstall/env` exports the persistent runtime
(the same SHA as the install and configure steps):

```yaml
- uses: lmctl-ai/lminstall/env@5054af1f6a10198b362ea51b3502504c17915f65
  with:
    provider: claude
    profile-dir: ${{ runner.temp }}/claude-profile
    runtime-dir: ${{ runner.temp }}/claude-runtime
# later steps inherit HOME, ANTHROPIC_BASE_URL, ... and can run `lmctl seed`/`lmctl prompt`
```

The `launch-mock` workflow runs install → configure → run → exact reply/assertion
with real tools on Ubuntu, macOS and Windows, a checksummed lmmock release and a
wrong-expectation failing control. Live keys never enter this matrix. Installation
CI remains a separate full-catalog lifecycle check. Offline launch controls:
`go run scripts/mutate-launch.go`.
Native updater artifacts are verified at a private staging pathname before replacing the installed executable. A rejected candidate leaves the working binary and receipt in place. Archive extraction uses a directory-root handle and validates the gzip trailer; a pre-existing symlink cannot redirect a file outside that root. Windows Hermes PortableGit uses the official SHA-addressed mirror only for availability failures, with the same digest.

Hermes source upgrades build in a new owned checkout and verify the selected stable version before selecting its launcher, just like a versioned release bundle. A failed completion leaves the prior checkout and receipt usable. `bin_dir` is the stable owned `bin` directory; upgrading a legacy source-path install may require refreshing PATH from that reported value. Earlier verified checkouts remain owned until uninstall so a failed candidate cannot mutate them. Native artifact verification output is reused when writing the receipt; the installer does not execute the artifact again after replacing the old pathname.

## Aggregate lmctl and lmscript from GitHub

Set `lmctl-release: latest` (or an exact `vBASE-github.RUN.ATTEMPT` tag) on this
Action; `providers` may be omitted. Both launchers and the complete package tree
are installed under the owned `root`, using Node 24 from setup-node. The Action
exports `LMCTL_PACKAGE_ROOT`, `LMSCRIPT_PACKAGE_ROOT`, `LMSCRIPT_BIN` and verified
package-root/version outputs. It never installs lmctl through a registry.

The source is private `lmctlhq/lmctl-mono`. A workflow in that repository can use
its own token. Other lmctl-ai/lmctlhq repositories must pass `release-token` with
Contents:read access to mono (their automatic repository token cannot cross that
boundary). No token is saved in receipts or sent to asset redirect hosts. Trusted
release integration can also pass `lmctl-candidate`, a manifest of exact asset IDs
and hashes, to install an unpublished candidate; do not accept that path from an
untrusted pull request. Outside Actions, provision Node >=24.15 and run
`lminstall lmctl --root ABS --release latest`, with `LMCTL_RELEASE_TOKEN` in the
environment. Repeat installs verify the recorded tree; changed files are refused.
