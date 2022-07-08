package service

import (
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/protobuf/ptypes/wrappers"
	"golang.org/x/net/context"

	"kubevirt.io/csi-driver/pkg/kubevirt"
)

//IdentityService of kubevirt-csi-driver
type IdentityService struct {
	infraClusterClient kubevirt.Client
}

//GetPluginInfo returns the vendor name and version - set in build time
func (i *IdentityService) GetPluginInfo(context.Context, *csi.GetPluginInfoRequest) (*csi.GetPluginInfoResponse, error) {
	return &csi.GetPluginInfoResponse{
		Name:          VendorName,
		VendorVersion: VendorVersion,
	}, nil
}

//GetPluginCapabilities declares the plugins capabilities
func (i *IdentityService) GetPluginCapabilities(context.Context, *csi.GetPluginCapabilitiesRequest) (*csi.GetPluginCapabilitiesResponse, error) {
	return &csi.GetPluginCapabilitiesResponse{
		Capabilities: []*csi.PluginCapability{
			{
				Type: &csi.PluginCapability_Service_{
					Service: &csi.PluginCapability_Service{
						Type: csi.PluginCapability_Service_CONTROLLER_SERVICE,
					},
				},
			},
		},
	}, nil
}

// Probe checks the state of the connection to kubevirt API
func (i *IdentityService) Probe(ctx context.Context, _ *csi.ProbeRequest) (*csi.ProbeResponse, error) {
	if err := i.infraClusterClient.Ping(ctx); err != nil {
		return nil, err
	}
	return &csi.ProbeResponse{Ready: &wrappers.BoolValue{Value: true}}, nil
}
