package cpaapikeys

// ManagementAPIKeysResponse 是 CPA /v0/management/api-keys 响应 DTO。
type ManagementAPIKeysResponse struct {
	APIKeys []string `json:"api-keys"`
}

// MutationResponse 是 CPA api-keys 写接口的通用确认响应。
type MutationResponse struct {
	Status string `json:"status"`
}

// replaceRequest 是 PUT /v0/management/api-keys 的全量替换请求体。
type replaceRequest struct {
	Items []string `json:"items"`
}

// updateRequest 是 PATCH /v0/management/api-keys 的单键替换请求体。
type updateRequest struct {
	Old string `json:"old"`
	New string `json:"new"`
}

// ReplaceRequest 构建 PUT 请求体，导出供 client 包组装。
func ReplaceRequest(keys []string) replaceRequest {
	return replaceRequest{Items: keys}
}

// UpdateRequest 构建 PATCH 请求体，导出供 client 包组装。
func UpdateRequest(oldKey, newKey string) updateRequest {
	return updateRequest{Old: oldKey, New: newKey}
}
