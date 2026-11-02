# Launch adapter goldens

File bytes and environment values were ported from the working WP129
`provider-mock-e2e` configure function at 4eecd545. JSON has canonical key order;
TOML bytes retain the original spelling. Environment fixtures exclude the
caller's OS/PATH variables. Paths use /RUNTIME/home and /INSTALL for portable
comparison, and credentials are the literal nonsecret PLACEHOLDER.

Intentional changes: Claude pins all default model slots; Hermes uses the owned
runtime tool directory and an environment key instead of a secret argv option;
DSH's native JSON/YAML patch selects the requested model instead of its vendor
default. Preview and runtime receive equivalent config with different private
paths, endpoint and random placeholder. No provider executable is replaced by
these fixtures; separate real-tool Actions jobs qualify the actual API request.
