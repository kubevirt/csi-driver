package main

import (
	"testing"
)

func TestResolveNodeID(t *testing.T) {
	tests := []struct {
		name        string
		nodeName    string
		providerID  string
		annotations map[string]string
		wantNodeID  string
		wantErr     bool
	}{
		{
			name:       "kubevirt providerID with annotation",
			nodeName:   "worker-0",
			providerID: "kubevirt://my-vm",
			annotations: map[string]string{
				"cluster.x-k8s.io/cluster-namespace": "my-namespace",
			},
			wantNodeID: "my-namespace/my-vm",
			wantErr:    false,
		},
		{
			name:        "kubevirt providerID without annotation",
			nodeName:    "worker-0",
			providerID:  "kubevirt://my-vm",
			annotations: nil,
			wantNodeID:  "",
			wantErr:     true,
		},
		{
			name:        "kubevirt providerID with empty annotation",
			nodeName:    "worker-0",
			providerID:  "kubevirt://my-vm",
			annotations: map[string]string{},
			wantNodeID:  "",
			wantErr:     true,
		},
		{
			name:       "empty providerID with annotations",
			nodeName:   "worker-0",
			providerID: "",
			annotations: map[string]string{
				"csi.kubevirt.io/infra-vm-name":      "annotated-vm",
				"csi.kubevirt.io/infra-vm-namespace": "annotated-namespace",
			},
			wantNodeID: "annotated-namespace/annotated-vm",
			wantErr:    false,
		},
		{
			name:       "non-kubevirt providerID with annotations",
			nodeName:   "worker-0",
			providerID: "baremetalhost:///openshift-machine-api/worker-0/uuid",
			annotations: map[string]string{
				"csi.kubevirt.io/infra-vm-name":      "annotated-vm",
				"csi.kubevirt.io/infra-vm-namespace": "annotated-namespace",
			},
			wantNodeID: "annotated-namespace/annotated-vm",
			wantErr:    false,
		},
		{
			name:        "non-kubevirt providerID without annotations",
			nodeName:    "worker-0",
			providerID:  "baremetalhost:///openshift-machine-api/worker-0/uuid",
			annotations: nil,
			wantNodeID:  "",
			wantErr:     true,
		},
		{
			name:        "empty providerID without annotations",
			nodeName:    "worker-0",
			providerID:  "",
			annotations: nil,
			wantNodeID:  "",
			wantErr:     true,
		},
		{
			name:       "empty providerID with partial annotations (missing namespace)",
			nodeName:   "worker-0",
			providerID: "",
			annotations: map[string]string{
				"csi.kubevirt.io/infra-vm-name": "annotated-vm",
			},
			wantNodeID: "",
			wantErr:    true,
		},
		{
			name:       "empty providerID with partial annotations (missing name)",
			nodeName:   "worker-0",
			providerID: "",
			annotations: map[string]string{
				"csi.kubevirt.io/infra-vm-namespace": "annotated-namespace",
			},
			wantNodeID: "",
			wantErr:    true,
		},
		{
			name:       "kubevirt providerID takes precedence over fallback annotations",
			nodeName:   "worker-0",
			providerID: "kubevirt://provider-vm",
			annotations: map[string]string{
				"cluster.x-k8s.io/cluster-namespace": "provider-namespace",
				"csi.kubevirt.io/infra-vm-name":      "annotated-vm",
				"csi.kubevirt.io/infra-vm-namespace": "annotated-namespace",
			},
			wantNodeID: "provider-namespace/provider-vm",
			wantErr:    false,
		},
		{
			name:       "aws providerID falls back to annotations",
			nodeName:   "worker-0",
			providerID: "aws:///us-east-1a/i-1234567890abcdef0",
			annotations: map[string]string{
				"csi.kubevirt.io/infra-vm-name":      "annotated-vm",
				"csi.kubevirt.io/infra-vm-namespace": "annotated-namespace",
			},
			wantNodeID: "annotated-namespace/annotated-vm",
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotNodeID, err := resolveNodeID(tt.nodeName, tt.providerID, tt.annotations)

			if (err != nil) != tt.wantErr {
				t.Errorf("resolveNodeID() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if gotNodeID != tt.wantNodeID {
				t.Errorf("resolveNodeID() = %v, want %v", gotNodeID, tt.wantNodeID)
			}

			// Verify error message contains useful information when error is expected
			if tt.wantErr && err != nil {
				errMsg := err.Error()
				if !contains(errMsg, tt.nodeName) {
					t.Errorf("error message should contain node name %q, got: %v", tt.nodeName, errMsg)
				}
				if !contains(errMsg, "csi.kubevirt.io/infra-vm-name") {
					t.Errorf("error message should mention the annotation csi.kubevirt.io/infra-vm-name, got: %v", errMsg)
				}
				if !contains(errMsg, "csi.kubevirt.io/infra-vm-namespace") {
					t.Errorf("error message should mention the annotation csi.kubevirt.io/infra-vm-namespace, got: %v", errMsg)
				}
				if !contains(errMsg, "restart") {
					t.Errorf("error message should mention restarting the pod, got: %v", errMsg)
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
