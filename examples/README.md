# Examples

Runnable examples for installing coding CLIs with lminstall and testing them
against [lmmock](https://github.com/lmctl-ai/lmmock)'s built-in LLM API mock. None needs an
account, API key or model access, and every script is exercised in CI
(`.github/workflows/examples.yml`).

| File | What it shows |
| --- | --- |
| [`mock-run.sh`](mock-run.sh) | Install a provider, run the **real** CLI against the mock with the right API key (exact reply) and with a **wrong** key (authentication error, mock logs a 401/403). `claude`, `codex`, `gemini` and `pi`. |
| [`orchestrator-env.sh`](orchestrator-env.sh) | `lminstall env`: render a persistent runtime directory and export its environment, for an orchestrator that launches the CLI itself many times. |
| [`github-actions-consumer.yml`](github-actions-consumer.yml) | A consumer repository's workflow: the install Action, the configure Action, a checksum-verified lmmock download, and a real-CLI smoke test on Linux and macOS. |

```sh
go build -o lminstall ./cmd/lminstall          # or download a release binary
# lmmock: a release from https://github.com/lmctl-ai/lmmock/releases (verify SHA256SUMS)
LMINSTALL=./lminstall LMMOCK=./lmmock examples/mock-run.sh claude
LMINSTALL=./lminstall LMMOCK=./lmmock examples/orchestrator-env.sh claude
```

Notes:

* The scripts install into a throwaway prefix (`LMINSTALL_ROOT`) and delete it on exit.
* `lmmock exec` prints a short `LMCODE_RETURN_*` footer when it finishes; that is
  lmmock, not a failure.
* Claude Code prints a harmless `unrecognized_model` line for the made-up model
  name `mock-agent`.
* The mock config the scripts use is
  `{"text":"…","toolCalls":false,"anthropicBearer":true,"apiKey":"…"}`: plain replies even
  though the CLI declares tools, Claude Code's bearer-token login accepted, and a
  required key. More ready-made lmmock configurations (scripted turns, rate limits,
  server errors, Codex device-login modes) are listed in lmmock's `docs/llm-api.md`.
