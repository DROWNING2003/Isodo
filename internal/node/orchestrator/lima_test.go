package orchestrator

import (
	"testing"

	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestValidateRequestTemplateSelection(t *testing.T) {
	server := new(LimaServer)
	tests := []struct {
		name        string
		request     *nodev1.ApplyOperationRequest
		wantCode    codes.Code
		wantFailure bool
	}{
		{
			name:    "base template default",
			request: &nodev1.ApplyOperationRequest{Action: nodev1.OperationAction_OPERATION_ACTION_CREATE},
		},
		{
			name: "fixed template build",
			request: &nodev1.ApplyOperationRequest{
				Action: nodev1.OperationAction_OPERATION_ACTION_CREATE, TemplateId: "template-1", TemplateBuildId: "build-1",
			},
		},
		{
			name: "template ID without build ID",
			request: &nodev1.ApplyOperationRequest{
				Action: nodev1.OperationAction_OPERATION_ACTION_CREATE, TemplateId: "template-1",
			},
			wantCode: codes.InvalidArgument, wantFailure: true,
		},
		{
			name: "build ID without template ID",
			request: &nodev1.ApplyOperationRequest{
				Action: nodev1.OperationAction_OPERATION_ACTION_CREATE, TemplateBuildId: "build-1",
			},
			wantCode: codes.InvalidArgument, wantFailure: true,
		},
		{
			name: "template selection on start",
			request: &nodev1.ApplyOperationRequest{
				Action: nodev1.OperationAction_OPERATION_ACTION_START, TemplateId: "template-1", TemplateBuildId: "build-1",
			},
			wantCode: codes.InvalidArgument, wantFailure: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := server.validateRequest(tt.request)
			if tt.wantFailure {
				if status.Code(err) != tt.wantCode {
					t.Fatalf("validateRequest() error code = %s, want %s; error=%v", status.Code(err), tt.wantCode, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateRequest() error = %v", err)
			}
		})
	}
}
