package service

import (
	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/net/context"
	wrappers "google.golang.org/protobuf/types/known/wrapperspb"
	"k8s.io/client-go/kubernetes"
)

type connectivityProbeInterface interface {
	Probe() error
}

type connectivityProbe struct {
	clientset kubernetes.Interface
}

func (p *connectivityProbe) Probe() error {
	_, err := p.clientset.Discovery().ServerVersion()
	return err
}

func NewIdentityService(clientset kubernetes.Interface) *IdentityService {
	return &IdentityService{
		connectivityProbe: &connectivityProbe{
			clientset: clientset,
		},
	}
}

// IdentityService of kubevirt-csi-driver
type IdentityService struct {
	csi.UnimplementedIdentityServer
	connectivityProbe connectivityProbeInterface
}

var _ csi.IdentityServer = &IdentityService{}

// GetPluginInfo returns the vendor name and version - set in build time
func (i *IdentityService) GetPluginInfo(context.Context, *csi.GetPluginInfoRequest) (*csi.GetPluginInfoResponse, error) {
	return &csi.GetPluginInfoResponse{
		Name:          VendorName,
		VendorVersion: VendorVersion,
	}, nil
}

// GetPluginCapabilities declares the plugins capabilities
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

// Probe checks the state of the connection to kubernetes API
func (i *IdentityService) Probe(ctx context.Context, _ *csi.ProbeRequest) (*csi.ProbeResponse, error) {
	err := i.connectivityProbe.Probe()
	if err != nil {
		return nil, err
	}
	return &csi.ProbeResponse{Ready: &wrappers.BoolValue{Value: true}}, nil
}
