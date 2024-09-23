package rpadmin

import (
	"context"
	"net/http"
)

const (
	baseMountEndpoint   = "/v1/topics/mount"
	baseUnmountEndpoint = "/v1/topics/unmount"
)

// NamespacedTopic represents a topic with an optional namespace
type NamespacedTopic struct {
	Topic     string  `json:"topic"`
	Namespace *string `json:"ns,omitempty"`
}

// InboundTopic represents a topic to be mounted
type InboundTopic struct {
	SourceTopic NamespacedTopic  `json:"source_topic"`
	Alias       *NamespacedTopic `json:"alias,omitempty"`
}

// MountConfiguration represents the configuration for mounting topics
type MountConfiguration struct {
	Topics []InboundTopic `json:"topics"`
}

// MountTopics mounts topics according to the provided configuration
func (a *AdminAPI) MountTopics(ctx context.Context, config MountConfiguration) (MigrationInfo, error) {
	var response MigrationInfo
	err := a.sendAny(ctx, http.MethodPost, baseMountEndpoint, config, &response)
	return response, err
}

// UnmountConfiguration represents the configuration for unmounting topics
type UnmountConfiguration struct {
	Topics []NamespacedTopic `json:"topics"`
}

// UnmountTopics unmounts the provided list of topics
func (a *AdminAPI) UnmountTopics(ctx context.Context, config UnmountConfiguration) (MigrationInfo, error) {
	var response MigrationInfo
	err := a.sendAny(ctx, http.MethodPost, baseUnmountEndpoint, config, &response)
	return response, err
}

// MigrationInfo represents the underlying migration information returned by mount and unmount operations
type MigrationInfo struct {
	ID int `json:"id"`
}
