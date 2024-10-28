package rpadmin

import (
	"context"
	"net/http"
)

const (
	baseMountEndpoint     = "/v1/topics/mount"
	baseUnmountEndpoint   = "/v1/topics/unmount"
	baseMountableEndpoint = "/v1/topics/mountable"
)

// NamespacedTopic represents a topic with an optional namespace
type NamespacedTopic struct {
	Topic     string  `json:"topic"`
	Namespace *string `json:"ns,omitempty"`
}

// InboundTopic represents a topic to be mounted
type InboundTopic struct {
	SourceTopicReference NamespacedTopic  `json:"source_topic_reference"`
	Alias                *NamespacedTopic `json:"alias,omitempty"`
}

// NamespacedOrInboundTopic is a composite struct of NamespacedTopic and InboundTopic.
type NamespacedOrInboundTopic struct {
	NamespacedTopic
	InboundTopic
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

// MountableTopic represents a topic's location in cloud storage
type MountableTopic struct {
	// TopicLocation is the unique topic location in cloud storage with the format:
	// <topic name>/<cluster_uuid>/<initial_revision>
	TopicLocation string `json:"topic_location"`

	// Topic is the name of the topic
	Topic string `json:"topic"`

	// Namespace is the topic namespace. If not present it is assumed that
	// topic is in the "kafka" namespace
	Namespace *string `json:"ns,omitempty"`
}

// ListMountableTopicsResponse represents the response containing a list of mountable topics
type ListMountableTopicsResponse struct {
	Topics []MountableTopic `json:"topics"`
}

// ListMountableTopics retrieves a list of topics that can be mounted from cloud storage
func (a *AdminAPI) ListMountableTopics(ctx context.Context) (ListMountableTopicsResponse, error) {
	var response ListMountableTopicsResponse
	err := a.sendAny(ctx, http.MethodGet, baseMountableEndpoint, nil, &response)
	return response, err
}
