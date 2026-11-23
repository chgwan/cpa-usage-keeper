package cpa

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// KeyModelAccessStatus mirrors GET /v0/management/plugins/key-model-access/status.
type KeyModelAccessStatus struct {
	Plugin                string `json:"plugin"`
	Version               string `json:"version"`
	SchemaVersion         int    `json:"schema_version"`
	PolicySchemaVersion   int    `json:"policy_schema_version"`
	HostSchemaVersion     int    `json:"host_schema_version"`
	AuthMode              string `json:"auth_mode"`
	IdentitySource        string `json:"identity_source"`
	UnconfiguredKeyAction string `json:"unconfigured_key_action"`
	LastError             string `json:"last_error"`
	PolicyCount           int    `json:"policy_count"`
	Revision              uint64 `json:"revision"`
	PolicyFile            string `json:"policy_file"`
	PersistentUpdates     bool   `json:"persistent_updates"`
	Source                string `json:"source"`
	UpdatedAt             string `json:"updated_at"`
	FailClosed            bool   `json:"fail_closed"`
}

// KeyModelAccessPolicyItem represents one caller_scope rule in v2 policy schema.
type KeyModelAccessPolicyItem struct {
	CallerScope string   `json:"caller_scope"`
	AllowModels []string `json:"allow_models"`
	DenyModels  []string `json:"deny_models"`
}

// KeyModelAccessPolicyDocument is the v2 policy payload accepted by PUT /policies.
type KeyModelAccessPolicyDocument struct {
	Version  int                        `json:"version"`
	Policies []KeyModelAccessPolicyItem `json:"policies"`
}

// KeyModelAccessPoliciesResponse mirrors GET /v0/management/plugins/key-model-access/policies.
type KeyModelAccessPoliciesResponse struct {
	Policy    KeyModelAccessPolicyDocument `json:"policy"`
	Revision  uint64                       `json:"revision"`
	Source    string                       `json:"source"`
	UpdatedAt string                       `json:"updated_at"`
}

// KeyModelAccessReplacePoliciesResponse mirrors PUT /v0/management/plugins/key-model-access/policies.
type KeyModelAccessReplacePoliciesResponse struct {
	OK          bool                         `json:"ok"`
	PolicyCount int                          `json:"policy_count"`
	Persistent  bool                         `json:"persistent"`
	Revision    uint64                       `json:"revision"`
	Policy      KeyModelAccessPolicyDocument `json:"policy"`
}

// KeyModelAccessModelsResponse mirrors GET /v0/management/plugins/key-model-access/models.
type KeyModelAccessModelsResponse struct {
	Models []string `json:"models"`
}

// KeyModelAccessReloadResponse mirrors POST /v0/management/plugins/key-model-access/reload.
type KeyModelAccessReloadResponse struct {
	OK          bool   `json:"ok"`
	PolicyCount int    `json:"policy_count"`
	Revision    uint64 `json:"revision"`
}

// KeyModelAccessInitStorageResponse mirrors POST /v0/management/plugins/key-model-access/initialize-storage.
type KeyModelAccessInitStorageResponse struct {
	Created     bool   `json:"created"`
	PolicyFile  string `json:"policy_file"`
	PolicyCount int    `json:"policy_count"`
}

// ManagementPluginsListResponse mirrors the fields Keeper consumes from GET /v0/management/plugins.
type ManagementPluginsListResponse struct {
	PluginsDir string `json:"plugins_dir"`
}

// IsHTTPStatus reports whether err wraps an upstream CPA HTTP status error matching status.
func IsHTTPStatus(err error, status int) bool {
	var statusErr *httpStatusError
	return errors.As(err, &statusErr) && statusErr.statusCode == status
}

var pluginsDirEntityReplacer = strings.NewReplacer(
	"&amp;", "&",
	"&lt;", "<",
	"&gt;", ">",
	"&#34;", `"`,
	"&#39;", "'",
)

// DecodePluginsDir decodes the five HTML entities Go's html.EscapeString emits in
// /v0/management/plugins.plugins_dir in a single pass, matching key-model-access Web UI.
func DecodePluginsDir(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	return pluginsDirEntityReplacer.Replace(trimmed)
}

func (c *Client) FetchKeyModelAccessStatus(ctx context.Context) (KeyModelAccessStatus, int, error) {
	var payload KeyModelAccessStatus
	statusCode, _, err := c.doManagementJSONRequest(ctx, cpaManagementKeyModelAccessStatusEndpoint, &payload, "key-model-access status")
	return payload, statusCode, err
}

func (c *Client) FetchKeyModelAccessModels(ctx context.Context) ([]string, int, error) {
	var payload KeyModelAccessModelsResponse
	statusCode, _, err := c.doManagementJSONRequest(ctx, cpaManagementKeyModelAccessModelsEndpoint, &payload, "key-model-access models")
	if err != nil {
		return nil, statusCode, err
	}
	if payload.Models == nil {
		return []string{}, statusCode, nil
	}
	return payload.Models, statusCode, nil
}

func (c *Client) FetchKeyModelAccessPolicies(ctx context.Context) (KeyModelAccessPoliciesResponse, string, int, error) {
	var payload KeyModelAccessPoliciesResponse
	statusCode, _, headers, err := c.doManagementJSONRequestWithHeaders(
		ctx, http.MethodGet, cpaManagementKeyModelAccessPoliciesEndpoint, nil, &payload, "key-model-access policies", nil,
	)
	if err != nil {
		return payload, "", statusCode, err
	}
	if payload.Policy.Policies == nil {
		payload.Policy.Policies = []KeyModelAccessPolicyItem{}
	}
	return payload, headers.Get("ETag"), statusCode, nil
}

func (c *Client) ReplaceKeyModelAccessPolicies(ctx context.Context, document KeyModelAccessPolicyDocument, ifMatch string) (KeyModelAccessReplacePoliciesResponse, int, error) {
	if document.Version == 0 {
		document.Version = 2
	}
	if document.Policies == nil {
		document.Policies = []KeyModelAccessPolicyItem{}
	}
	for index := range document.Policies {
		if document.Policies[index].AllowModels == nil {
			document.Policies[index].AllowModels = []string{}
		}
		if document.Policies[index].DenyModels == nil {
			document.Policies[index].DenyModels = []string{}
		}
	}
	var headers http.Header
	if trimmed := strings.TrimSpace(ifMatch); trimmed != "" {
		headers = http.Header{"If-Match": []string{trimmed}}
	}
	var payload KeyModelAccessReplacePoliciesResponse
	statusCode, _, _, err := c.doManagementJSONRequestWithHeaders(
		ctx, http.MethodPut, cpaManagementKeyModelAccessPoliciesEndpoint, document, &payload, "key-model-access policies replace", headers,
	)
	return payload, statusCode, err
}

func (c *Client) ReloadKeyModelAccessPolicies(ctx context.Context) (KeyModelAccessReloadResponse, int, error) {
	var payload KeyModelAccessReloadResponse
	statusCode, _, err := c.doManagementJSONPostRequest(ctx, cpaManagementKeyModelAccessReloadEndpoint, nil, &payload, "key-model-access reload")
	return payload, statusCode, err
}

func (c *Client) FetchManagementPluginsList(ctx context.Context) (ManagementPluginsListResponse, int, error) {
	var payload ManagementPluginsListResponse
	statusCode, _, err := c.doManagementJSONRequest(ctx, cpaManagementPluginsEndpoint, &payload, "plugins list")
	return payload, statusCode, err
}

func (c *Client) InitializeKeyModelAccessStorage(ctx context.Context, pluginsDir string) (KeyModelAccessInitStorageResponse, int, error) {
	trimmed := strings.TrimSpace(pluginsDir)
	if trimmed == "" {
		return KeyModelAccessInitStorageResponse{}, 0, fmt.Errorf("plugins_dir is required")
	}
	var payload KeyModelAccessInitStorageResponse
	statusCode, _, err := c.doManagementJSONPostRequest(ctx, cpaManagementKeyModelAccessInitStorageEndpoint, struct {
		PluginsDir string `json:"plugins_dir"`
	}{PluginsDir: trimmed}, &payload, "key-model-access initialize storage")
	return payload, statusCode, err
}

func (c *Client) PatchKeyModelAccessPluginConfig(ctx context.Context, policyFile string) (int, error) {
	trimmed := strings.TrimSpace(policyFile)
	if trimmed == "" {
		return 0, fmt.Errorf("policy_file is required")
	}
	statusCode, _, err := c.doManagementJSONRequestWithBody(ctx, http.MethodPatch, cpaManagementKeyModelAccessConfigEndpoint, struct {
		PolicyFile string `json:"policy_file"`
	}{PolicyFile: trimmed}, nil, "key-model-access plugin config")
	return statusCode, err
}
