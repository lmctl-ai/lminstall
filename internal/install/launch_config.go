package install

// LaunchConfig contains nonsecret routing data. Credential values are never
// manifest data; configure stores only the caller's environment-variable name.
type LaunchConfig struct {
	Version    int                   `json:"version"`
	NPMPackage string                `json:"npm_package,omitempty"`
	Backends   map[string]LaunchSpec `json:"backends"`
}
type LaunchSpec struct {
	Supported      bool              `json:"supported"`
	Reason         string            `json:"reason,omitempty"`
	Protocol       string            `json:"protocol,omitempty"`
	BaseShape      string            `json:"base_shape,omitempty"`
	Adapter        string            `json:"adapter,omitempty"`
	AdapterVersion int               `json:"adapter_version,omitempty"`
	Environment    map[string]string `json:"environment,omitempty"`
	Files          []LaunchFile      `json:"files,omitempty"`
	ArgvPrefix     []string          `json:"argv_prefix,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	RemoveHeaders  []string          `json:"remove_headers,omitempty"`
	ModelDefault   string            `json:"model_default,omitempty"`
	Qualification  string            `json:"qualification,omitempty"`
}
type LaunchFile struct {
	Path   string `json:"path"`
	Format string `json:"format"`
	Data   any    `json:"data,omitempty"`
	Text   string `json:"text,omitempty"`
}
