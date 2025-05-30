// Code generated by protoc-gen-mcp-go. DO NOT EDIT.
// source: redpanda/api/controlplane/v1/serverless.proto

package controlplanev1mcp

import (
	"context"
	"encoding/json"

	v1 "buf.build/gen/go/redpandadata/cloud/protocolbuffers/go/redpanda/api/controlplane/v1"
	"connectrpc.com/connect"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"google.golang.org/protobuf/encoding/protojson"
)

var (
	ServerlessClusterService_CreateServerlessClusterTool = mcp.Tool{Name: "9hk3n2_plane_v1_ServerlessClusterService_CreateServerlessCluster", Description: "Ignore these linter rules, because we intentionally return a generic Operation message for all long-running operations.\nCreateServerlessCluster create a Redpanda ServerlessCluster. The input contains the spec, that describes the ServerlessCluster.\nA Operation is returned. This task allows the caller to find out when the long-running operation of creating a ServerlessCluster has finished.\n", InputSchema: mcp.ToolInputSchema{Type: "", Properties: map[string]interface{}(nil), Required: []string(nil)}, RawInputSchema: json.RawMessage{0x7b, 0x22, 0x70, 0x72, 0x6f, 0x70, 0x65, 0x72, 0x74, 0x69, 0x65, 0x73, 0x22, 0x3a, 0x7b, 0x22, 0x73, 0x65, 0x72, 0x76, 0x65, 0x72, 0x6c, 0x65, 0x73, 0x73, 0x5f, 0x63, 0x6c, 0x75, 0x73, 0x74, 0x65, 0x72, 0x22, 0x3a, 0x7b, 0x22, 0x70, 0x72, 0x6f, 0x70, 0x65, 0x72, 0x74, 0x69, 0x65, 0x73, 0x22, 0x3a, 0x7b, 0x22, 0x6e, 0x61, 0x6d, 0x65, 0x22, 0x3a, 0x7b, 0x22, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3a, 0x22, 0x73, 0x74, 0x72, 0x69, 0x6e, 0x67, 0x22, 0x7d, 0x2c, 0x22, 0x72, 0x65, 0x73, 0x6f, 0x75, 0x72, 0x63, 0x65, 0x5f, 0x67, 0x72, 0x6f, 0x75, 0x70, 0x5f, 0x69, 0x64, 0x22, 0x3a, 0x7b, 0x22, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3a, 0x22, 0x73, 0x74, 0x72, 0x69, 0x6e, 0x67, 0x22, 0x7d, 0x2c, 0x22, 0x73, 0x65, 0x72, 0x76, 0x65, 0x72, 0x6c, 0x65, 0x73, 0x73, 0x5f, 0x72, 0x65, 0x67, 0x69, 0x6f, 0x6e, 0x22, 0x3a, 0x7b, 0x22, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3a, 0x22, 0x73, 0x74, 0x72, 0x69, 0x6e, 0x67, 0x22, 0x7d, 0x7d, 0x2c, 0x22, 0x72, 0x65, 0x71, 0x75, 0x69, 0x72, 0x65, 0x64, 0x22, 0x3a, 0x5b, 0x22, 0x6e, 0x61, 0x6d, 0x65, 0x22, 0x2c, 0x22, 0x72, 0x65, 0x73, 0x6f, 0x75, 0x72, 0x63, 0x65, 0x5f, 0x67, 0x72, 0x6f, 0x75, 0x70, 0x5f, 0x69, 0x64, 0x22, 0x2c, 0x22, 0x73, 0x65, 0x72, 0x76, 0x65, 0x72, 0x6c, 0x65, 0x73, 0x73, 0x5f, 0x72, 0x65, 0x67, 0x69, 0x6f, 0x6e, 0x22, 0x5d, 0x2c, 0x22, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3a, 0x22, 0x6f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x22, 0x7d, 0x7d, 0x2c, 0x22, 0x72, 0x65, 0x71, 0x75, 0x69, 0x72, 0x65, 0x64, 0x22, 0x3a, 0x5b, 0x5d, 0x2c, 0x22, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3a, 0x22, 0x6f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x22, 0x7d}}
	ServerlessClusterService_DeleteServerlessClusterTool = mcp.Tool{Name: "50n3gc_plane_v1_ServerlessClusterService_DeleteServerlessCluster", Description: "Ignore these linter rules, because we intentionally return a generic Operation message for all long-running operations.\n", InputSchema: mcp.ToolInputSchema{Type: "", Properties: map[string]interface{}(nil), Required: []string(nil)}, RawInputSchema: json.RawMessage{0x7b, 0x22, 0x70, 0x72, 0x6f, 0x70, 0x65, 0x72, 0x74, 0x69, 0x65, 0x73, 0x22, 0x3a, 0x7b, 0x22, 0x69, 0x64, 0x22, 0x3a, 0x7b, 0x22, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3a, 0x22, 0x73, 0x74, 0x72, 0x69, 0x6e, 0x67, 0x22, 0x7d, 0x7d, 0x2c, 0x22, 0x72, 0x65, 0x71, 0x75, 0x69, 0x72, 0x65, 0x64, 0x22, 0x3a, 0x5b, 0x5d, 0x2c, 0x22, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3a, 0x22, 0x6f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x22, 0x7d}}
	ServerlessClusterService_DummyCreateMetadataTool     = mcp.Tool{Name: "s22pi3_trolplane_v1_ServerlessClusterService_DummyCreateMetadata", Description: "Force openapi generator to generate the CreateServerlessClusterMetadata, so we can use it in OpenAPI schema.\n", InputSchema: mcp.ToolInputSchema{Type: "", Properties: map[string]interface{}(nil), Required: []string(nil)}, RawInputSchema: json.RawMessage{0x7b, 0x22, 0x70, 0x72, 0x6f, 0x70, 0x65, 0x72, 0x74, 0x69, 0x65, 0x73, 0x22, 0x3a, 0x7b, 0x22, 0x73, 0x65, 0x72, 0x76, 0x65, 0x72, 0x6c, 0x65, 0x73, 0x73, 0x5f, 0x63, 0x6c, 0x75, 0x73, 0x74, 0x65, 0x72, 0x22, 0x3a, 0x7b, 0x22, 0x70, 0x72, 0x6f, 0x70, 0x65, 0x72, 0x74, 0x69, 0x65, 0x73, 0x22, 0x3a, 0x7b, 0x22, 0x6e, 0x61, 0x6d, 0x65, 0x22, 0x3a, 0x7b, 0x22, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3a, 0x22, 0x73, 0x74, 0x72, 0x69, 0x6e, 0x67, 0x22, 0x7d, 0x2c, 0x22, 0x72, 0x65, 0x73, 0x6f, 0x75, 0x72, 0x63, 0x65, 0x5f, 0x67, 0x72, 0x6f, 0x75, 0x70, 0x5f, 0x69, 0x64, 0x22, 0x3a, 0x7b, 0x22, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3a, 0x22, 0x73, 0x74, 0x72, 0x69, 0x6e, 0x67, 0x22, 0x7d, 0x2c, 0x22, 0x73, 0x65, 0x72, 0x76, 0x65, 0x72, 0x6c, 0x65, 0x73, 0x73, 0x5f, 0x72, 0x65, 0x67, 0x69, 0x6f, 0x6e, 0x22, 0x3a, 0x7b, 0x22, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3a, 0x22, 0x73, 0x74, 0x72, 0x69, 0x6e, 0x67, 0x22, 0x7d, 0x7d, 0x2c, 0x22, 0x72, 0x65, 0x71, 0x75, 0x69, 0x72, 0x65, 0x64, 0x22, 0x3a, 0x5b, 0x22, 0x6e, 0x61, 0x6d, 0x65, 0x22, 0x2c, 0x22, 0x72, 0x65, 0x73, 0x6f, 0x75, 0x72, 0x63, 0x65, 0x5f, 0x67, 0x72, 0x6f, 0x75, 0x70, 0x5f, 0x69, 0x64, 0x22, 0x2c, 0x22, 0x73, 0x65, 0x72, 0x76, 0x65, 0x72, 0x6c, 0x65, 0x73, 0x73, 0x5f, 0x72, 0x65, 0x67, 0x69, 0x6f, 0x6e, 0x22, 0x5d, 0x2c, 0x22, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3a, 0x22, 0x6f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x22, 0x7d, 0x7d, 0x2c, 0x22, 0x72, 0x65, 0x71, 0x75, 0x69, 0x72, 0x65, 0x64, 0x22, 0x3a, 0x5b, 0x5d, 0x2c, 0x22, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3a, 0x22, 0x6f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x22, 0x7d}}
	ServerlessClusterService_DummyDeleteMetadataTool     = mcp.Tool{Name: "15y10z_trolplane_v1_ServerlessClusterService_DummyDeleteMetadata", Description: "Force openapi generator to generate the DeleteServerlessClusterMetadata, so we can use it in OpenAPI schema.\n", InputSchema: mcp.ToolInputSchema{Type: "", Properties: map[string]interface{}(nil), Required: []string(nil)}, RawInputSchema: json.RawMessage{0x7b, 0x22, 0x70, 0x72, 0x6f, 0x70, 0x65, 0x72, 0x74, 0x69, 0x65, 0x73, 0x22, 0x3a, 0x7b, 0x22, 0x69, 0x64, 0x22, 0x3a, 0x7b, 0x22, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3a, 0x22, 0x73, 0x74, 0x72, 0x69, 0x6e, 0x67, 0x22, 0x7d, 0x7d, 0x2c, 0x22, 0x72, 0x65, 0x71, 0x75, 0x69, 0x72, 0x65, 0x64, 0x22, 0x3a, 0x5b, 0x5d, 0x2c, 0x22, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3a, 0x22, 0x6f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x22, 0x7d}}
	ServerlessClusterService_GetServerlessClusterTool    = mcp.Tool{Name: "1rtyls_rolplane_v1_ServerlessClusterService_GetServerlessCluster", Description: "GetServerlessCluster retrieves the ServerlessCluster's information\n", InputSchema: mcp.ToolInputSchema{Type: "", Properties: map[string]interface{}(nil), Required: []string(nil)}, RawInputSchema: json.RawMessage{0x7b, 0x22, 0x70, 0x72, 0x6f, 0x70, 0x65, 0x72, 0x74, 0x69, 0x65, 0x73, 0x22, 0x3a, 0x7b, 0x22, 0x69, 0x64, 0x22, 0x3a, 0x7b, 0x22, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3a, 0x22, 0x73, 0x74, 0x72, 0x69, 0x6e, 0x67, 0x22, 0x7d, 0x2c, 0x22, 0x72, 0x65, 0x61, 0x64, 0x5f, 0x6d, 0x61, 0x73, 0x6b, 0x22, 0x3a, 0x7b, 0x22, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3a, 0x22, 0x73, 0x74, 0x72, 0x69, 0x6e, 0x67, 0x22, 0x7d, 0x7d, 0x2c, 0x22, 0x72, 0x65, 0x71, 0x75, 0x69, 0x72, 0x65, 0x64, 0x22, 0x3a, 0x5b, 0x22, 0x69, 0x64, 0x22, 0x5d, 0x2c, 0x22, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3a, 0x22, 0x6f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x22, 0x7d}}
	ServerlessClusterService_ListServerlessClustersTool  = mcp.Tool{Name: "thk7kp_lplane_v1_ServerlessClusterService_ListServerlessClusters", Description: "ListServerlessClusters lists ServerlessClusters.\n", InputSchema: mcp.ToolInputSchema{Type: "", Properties: map[string]interface{}(nil), Required: []string(nil)}, RawInputSchema: json.RawMessage{0x7b, 0x22, 0x70, 0x72, 0x6f, 0x70, 0x65, 0x72, 0x74, 0x69, 0x65, 0x73, 0x22, 0x3a, 0x7b, 0x22, 0x66, 0x69, 0x6c, 0x74, 0x65, 0x72, 0x22, 0x3a, 0x7b, 0x22, 0x70, 0x72, 0x6f, 0x70, 0x65, 0x72, 0x74, 0x69, 0x65, 0x73, 0x22, 0x3a, 0x7b, 0x22, 0x6e, 0x61, 0x6d, 0x65, 0x5f, 0x63, 0x6f, 0x6e, 0x74, 0x61, 0x69, 0x6e, 0x73, 0x22, 0x3a, 0x7b, 0x22, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3a, 0x22, 0x73, 0x74, 0x72, 0x69, 0x6e, 0x67, 0x22, 0x7d, 0x2c, 0x22, 0x72, 0x65, 0x73, 0x6f, 0x75, 0x72, 0x63, 0x65, 0x5f, 0x67, 0x72, 0x6f, 0x75, 0x70, 0x5f, 0x69, 0x64, 0x22, 0x3a, 0x7b, 0x22, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3a, 0x22, 0x73, 0x74, 0x72, 0x69, 0x6e, 0x67, 0x22, 0x7d, 0x2c, 0x22, 0x73, 0x65, 0x72, 0x76, 0x65, 0x72, 0x6c, 0x65, 0x73, 0x73, 0x5f, 0x72, 0x65, 0x67, 0x69, 0x6f, 0x6e, 0x22, 0x3a, 0x7b, 0x22, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3a, 0x22, 0x73, 0x74, 0x72, 0x69, 0x6e, 0x67, 0x22, 0x7d, 0x2c, 0x22, 0x73, 0x74, 0x61, 0x74, 0x65, 0x5f, 0x69, 0x6e, 0x22, 0x3a, 0x7b, 0x22, 0x65, 0x6e, 0x75, 0x6d, 0x22, 0x3a, 0x5b, 0x22, 0x53, 0x54, 0x41, 0x54, 0x45, 0x5f, 0x55, 0x4e, 0x53, 0x50, 0x45, 0x43, 0x49, 0x46, 0x49, 0x45, 0x44, 0x22, 0x2c, 0x22, 0x53, 0x54, 0x41, 0x54, 0x45, 0x5f, 0x50, 0x4c, 0x41, 0x43, 0x49, 0x4e, 0x47, 0x22, 0x2c, 0x22, 0x53, 0x54, 0x41, 0x54, 0x45, 0x5f, 0x43, 0x52, 0x45, 0x41, 0x54, 0x49, 0x4e, 0x47, 0x22, 0x2c, 0x22, 0x53, 0x54, 0x41, 0x54, 0x45, 0x5f, 0x52, 0x45, 0x41, 0x44, 0x59, 0x22, 0x2c, 0x22, 0x53, 0x54, 0x41, 0x54, 0x45, 0x5f, 0x44, 0x45, 0x4c, 0x45, 0x54, 0x49, 0x4e, 0x47, 0x22, 0x2c, 0x22, 0x53, 0x54, 0x41, 0x54, 0x45, 0x5f, 0x46, 0x41, 0x49, 0x4c, 0x45, 0x44, 0x22, 0x2c, 0x22, 0x53, 0x54, 0x41, 0x54, 0x45, 0x5f, 0x53, 0x55, 0x53, 0x50, 0x45, 0x4e, 0x44, 0x45, 0x44, 0x22, 0x5d, 0x2c, 0x22, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3a, 0x22, 0x73, 0x74, 0x72, 0x69, 0x6e, 0x67, 0x22, 0x7d, 0x7d, 0x2c, 0x22, 0x72, 0x65, 0x71, 0x75, 0x69, 0x72, 0x65, 0x64, 0x22, 0x3a, 0x5b, 0x5d, 0x2c, 0x22, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3a, 0x22, 0x6f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x22, 0x7d, 0x2c, 0x22, 0x70, 0x61, 0x67, 0x65, 0x5f, 0x73, 0x69, 0x7a, 0x65, 0x22, 0x3a, 0x7b, 0x22, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3a, 0x22, 0x69, 0x6e, 0x74, 0x65, 0x67, 0x65, 0x72, 0x22, 0x7d, 0x2c, 0x22, 0x70, 0x61, 0x67, 0x65, 0x5f, 0x74, 0x6f, 0x6b, 0x65, 0x6e, 0x22, 0x3a, 0x7b, 0x22, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3a, 0x22, 0x73, 0x74, 0x72, 0x69, 0x6e, 0x67, 0x22, 0x7d, 0x2c, 0x22, 0x72, 0x65, 0x61, 0x64, 0x5f, 0x6d, 0x61, 0x73, 0x6b, 0x22, 0x3a, 0x7b, 0x22, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3a, 0x22, 0x73, 0x74, 0x72, 0x69, 0x6e, 0x67, 0x22, 0x7d, 0x7d, 0x2c, 0x22, 0x72, 0x65, 0x71, 0x75, 0x69, 0x72, 0x65, 0x64, 0x22, 0x3a, 0x5b, 0x5d, 0x2c, 0x22, 0x74, 0x79, 0x70, 0x65, 0x22, 0x3a, 0x22, 0x6f, 0x62, 0x6a, 0x65, 0x63, 0x74, 0x22, 0x7d}}
)

// ServerlessClusterServiceServer is compatible with the grpc-go server interface.
type ServerlessClusterServiceServer interface {
	CreateServerlessCluster(ctx context.Context, req *v1.CreateServerlessClusterRequest) (*v1.CreateServerlessClusterOperation, error)
	DeleteServerlessCluster(ctx context.Context, req *v1.DeleteServerlessClusterRequest) (*v1.DeleteServerlessClusterOperation, error)
	DummyCreateMetadata(ctx context.Context, req *v1.CreateServerlessClusterRequest) (*v1.CreateServerlessClusterMetadata, error)
	DummyDeleteMetadata(ctx context.Context, req *v1.DeleteServerlessClusterRequest) (*v1.DeleteServerlessClusterMetadata, error)
	GetServerlessCluster(ctx context.Context, req *v1.GetServerlessClusterRequest) (*v1.GetServerlessClusterResponse, error)
	ListServerlessClusters(ctx context.Context, req *v1.ListServerlessClustersRequest) (*v1.ListServerlessClustersResponse, error)
}

func RegisterServerlessClusterServiceHandler(s *mcpserver.MCPServer, srv ServerlessClusterServiceServer) {
	s.AddTool(ServerlessClusterService_CreateServerlessClusterTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		marshaled, err := json.Marshal(request.Params.Arguments)
		if err != nil {
			return nil, err
		}

		var req v1.CreateServerlessClusterRequest
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(marshaled, &req); err != nil {
			return nil, err
		}

		resp, err := srv.CreateServerlessCluster(ctx, &req)
		if err != nil {
			return nil, err
		}

		marshaled, err = (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(resp)
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResultText(string(marshaled)), nil
	})
	s.AddTool(ServerlessClusterService_DeleteServerlessClusterTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		marshaled, err := json.Marshal(request.Params.Arguments)
		if err != nil {
			return nil, err
		}

		var req v1.DeleteServerlessClusterRequest
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(marshaled, &req); err != nil {
			return nil, err
		}

		resp, err := srv.DeleteServerlessCluster(ctx, &req)
		if err != nil {
			return nil, err
		}

		marshaled, err = (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(resp)
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResultText(string(marshaled)), nil
	})
	s.AddTool(ServerlessClusterService_DummyCreateMetadataTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		marshaled, err := json.Marshal(request.Params.Arguments)
		if err != nil {
			return nil, err
		}

		var req v1.CreateServerlessClusterRequest
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(marshaled, &req); err != nil {
			return nil, err
		}

		resp, err := srv.DummyCreateMetadata(ctx, &req)
		if err != nil {
			return nil, err
		}

		marshaled, err = (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(resp)
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResultText(string(marshaled)), nil
	})
	s.AddTool(ServerlessClusterService_DummyDeleteMetadataTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		marshaled, err := json.Marshal(request.Params.Arguments)
		if err != nil {
			return nil, err
		}

		var req v1.DeleteServerlessClusterRequest
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(marshaled, &req); err != nil {
			return nil, err
		}

		resp, err := srv.DummyDeleteMetadata(ctx, &req)
		if err != nil {
			return nil, err
		}

		marshaled, err = (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(resp)
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResultText(string(marshaled)), nil
	})
	s.AddTool(ServerlessClusterService_GetServerlessClusterTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		marshaled, err := json.Marshal(request.Params.Arguments)
		if err != nil {
			return nil, err
		}

		var req v1.GetServerlessClusterRequest
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(marshaled, &req); err != nil {
			return nil, err
		}

		resp, err := srv.GetServerlessCluster(ctx, &req)
		if err != nil {
			return nil, err
		}

		marshaled, err = (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(resp)
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResultText(string(marshaled)), nil
	})
	s.AddTool(ServerlessClusterService_ListServerlessClustersTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		marshaled, err := json.Marshal(request.Params.Arguments)
		if err != nil {
			return nil, err
		}

		var req v1.ListServerlessClustersRequest
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(marshaled, &req); err != nil {
			return nil, err
		}

		resp, err := srv.ListServerlessClusters(ctx, &req)
		if err != nil {
			return nil, err
		}

		marshaled, err = (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(resp)
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResultText(string(marshaled)), nil
	})
}

// ConnectServerlessClusterServiceClient is compatible with the connectrpc-go client interface.
type ConnectServerlessClusterServiceClient interface {
	CreateServerlessCluster(ctx context.Context, req *connect.Request[v1.CreateServerlessClusterRequest]) (*connect.Response[v1.CreateServerlessClusterOperation], error)
	DeleteServerlessCluster(ctx context.Context, req *connect.Request[v1.DeleteServerlessClusterRequest]) (*connect.Response[v1.DeleteServerlessClusterOperation], error)
	DummyCreateMetadata(ctx context.Context, req *connect.Request[v1.CreateServerlessClusterRequest]) (*connect.Response[v1.CreateServerlessClusterMetadata], error)
	DummyDeleteMetadata(ctx context.Context, req *connect.Request[v1.DeleteServerlessClusterRequest]) (*connect.Response[v1.DeleteServerlessClusterMetadata], error)
	GetServerlessCluster(ctx context.Context, req *connect.Request[v1.GetServerlessClusterRequest]) (*connect.Response[v1.GetServerlessClusterResponse], error)
	ListServerlessClusters(ctx context.Context, req *connect.Request[v1.ListServerlessClustersRequest]) (*connect.Response[v1.ListServerlessClustersResponse], error)
}

// Register connectrpc handler, to forward MCP calls to it.
func ForwardToConnectServerlessClusterServiceClient(s *mcpserver.MCPServer, client ConnectServerlessClusterServiceClient) {
	s.AddTool(ServerlessClusterService_CreateServerlessClusterTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		marshaled, err := json.Marshal(request.Params.Arguments)
		if err != nil {
			return nil, err
		}

		var req v1.CreateServerlessClusterRequest
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(marshaled, &req); err != nil {
			return nil, err
		}

		resp, err := client.CreateServerlessCluster(ctx, connect.NewRequest(&req))
		if err != nil {
			return nil, err
		}

		marshaled, err = (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(resp.Msg)
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResultText(string(marshaled)), nil
	})
	s.AddTool(ServerlessClusterService_DeleteServerlessClusterTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		marshaled, err := json.Marshal(request.Params.Arguments)
		if err != nil {
			return nil, err
		}

		var req v1.DeleteServerlessClusterRequest
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(marshaled, &req); err != nil {
			return nil, err
		}

		resp, err := client.DeleteServerlessCluster(ctx, connect.NewRequest(&req))
		if err != nil {
			return nil, err
		}

		marshaled, err = (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(resp.Msg)
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResultText(string(marshaled)), nil
	})
	s.AddTool(ServerlessClusterService_DummyCreateMetadataTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		marshaled, err := json.Marshal(request.Params.Arguments)
		if err != nil {
			return nil, err
		}

		var req v1.CreateServerlessClusterRequest
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(marshaled, &req); err != nil {
			return nil, err
		}

		resp, err := client.DummyCreateMetadata(ctx, connect.NewRequest(&req))
		if err != nil {
			return nil, err
		}

		marshaled, err = (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(resp.Msg)
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResultText(string(marshaled)), nil
	})
	s.AddTool(ServerlessClusterService_DummyDeleteMetadataTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		marshaled, err := json.Marshal(request.Params.Arguments)
		if err != nil {
			return nil, err
		}

		var req v1.DeleteServerlessClusterRequest
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(marshaled, &req); err != nil {
			return nil, err
		}

		resp, err := client.DummyDeleteMetadata(ctx, connect.NewRequest(&req))
		if err != nil {
			return nil, err
		}

		marshaled, err = (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(resp.Msg)
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResultText(string(marshaled)), nil
	})
	s.AddTool(ServerlessClusterService_GetServerlessClusterTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		marshaled, err := json.Marshal(request.Params.Arguments)
		if err != nil {
			return nil, err
		}

		var req v1.GetServerlessClusterRequest
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(marshaled, &req); err != nil {
			return nil, err
		}

		resp, err := client.GetServerlessCluster(ctx, connect.NewRequest(&req))
		if err != nil {
			return nil, err
		}

		marshaled, err = (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(resp.Msg)
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResultText(string(marshaled)), nil
	})
	s.AddTool(ServerlessClusterService_ListServerlessClustersTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		marshaled, err := json.Marshal(request.Params.Arguments)
		if err != nil {
			return nil, err
		}

		var req v1.ListServerlessClustersRequest
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(marshaled, &req); err != nil {
			return nil, err
		}

		resp, err := client.ListServerlessClusters(ctx, connect.NewRequest(&req))
		if err != nil {
			return nil, err
		}

		marshaled, err = (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(resp.Msg)
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResultText(string(marshaled)), nil
	})
}
