package service

import (
	"errors"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubevirtapiv1 "kubevirt.io/client-go/api/v1"
	cdiv1alpha1 "kubevirt.io/containerized-data-importer/pkg/apis/core/v1alpha1"
)

func TestCreateVolume_Success(t *testing.T) {
	client := &ControllerClientMock{t: t}
	controller := ControllerService{client, testInfraNamespace}

	response, err := controller.CreateVolume(nil, getCreateVolumeRequest())
	assert.Nil(t, err)

	assert.Equal(t, testVolumeName, response.GetVolume().GetVolumeId())
	assert.Equal(t, testDataVolumeUID, response.GetVolume().VolumeContext[serialParameter])
	assert.Equal(t, getBusType(), response.GetVolume().VolumeContext[busParameter])
	assert.Equal(t, testVolumeStorageSize, response.GetVolume().GetCapacityBytes())
}

func TestCreateVolume_SuccessBlockDevice(t *testing.T) {
	// Set mode to block device
	testVolumeMode = corev1.PersistentVolumeBlock

	client := &ControllerClientMock{t: t}
	controller := ControllerService{client, testInfraNamespace}

	_, err := controller.CreateVolume(nil, getCreateVolumeRequest()) // The call to client.CreateDataVolume will test volume mode
	assert.Nil(t, err)
}

func TestCreateVolume_CreateDataVolumeFail(t *testing.T) {
	client := &ControllerClientMock{t: t, FailCreateDataVolume: true}
	controller := ControllerService{client, testInfraNamespace}

	_, err := controller.CreateVolume(nil, getCreateVolumeRequest())
	assert.NotNil(t, err)
}

func TestCreateVolume_CustomBus(t *testing.T) {
	client := &ControllerClientMock{t: t}
	controller := ControllerService{client, testInfraNamespace}

	busTypeLocal := "virtio"
	testBusType = &busTypeLocal

	response, err := controller.CreateVolume(nil, getCreateVolumeRequest())
	assert.Nil(t, err)

	assert.Equal(t, response.GetVolume().GetVolumeContext()[busParameter], *testBusType)
}

func TestDeleteVolume_Success(t *testing.T) {
	client := &ControllerClientMock{t: t}
	controller := ControllerService{client, testInfraNamespace}

	_, err := controller.DeleteVolume(nil, getDeleteVolumeRequest())
	assert.Nil(t, err)
}

func TestDeleteVolume_Fail(t *testing.T) {
	client := &ControllerClientMock{t: t, FailDeleteDataVolume: true}
	controller := ControllerService{client, testInfraNamespace}

	_, err := controller.DeleteVolume(nil, getDeleteVolumeRequest())
	assert.NotNil(t, err)
}

func TestPublishVolume_Success(t *testing.T) {
	client := &ControllerClientMock{t: t}
	controller := ControllerService{client, testInfraNamespace}

	_, err := controller.ControllerPublishVolume(nil, getPublishVolumeRequest()) // AddVolumeToVM tests the hotplug request
	assert.Nil(t, err)
}

func TestUnpublishVolume_Success(t *testing.T) {
	client := &ControllerClientMock{t: t}
	controller := ControllerService{client, testInfraNamespace}

	_, err := controller.ControllerUnpublishVolume(nil, getUnpublishVolumeRequest())
	assert.Nil(t, err)
}

//
// The rest of the file is code used by the tests and tests infrastructure
//

var (
	testVolumeName                    = "pvc-3d8be521-6e4b-4a87-add4-1961bf62f4ea"
	testInfraStorageClassName         = "infra-storage"
	testVolumeStorageSize     int64   = 1024 * 1024 * 1024 * 3
	testInfraNamespace                = "tenant-cluster-2"
	testNodeID                        = "6FC9C805-B3A0-570B-9D1B-3B8B9CFC9FB7"
	testVmName                        = "test-vm"
	testVmUID                         = "6fc9c805-b3a0-570b-9d1b-3b8b9cfc9fb7"
	testDataVolumeUID                 = "2d0111d5-494f-4731-8f67-122b27d3c366"
	testVolumeMode                    = corev1.PersistentVolumeFilesystem
	testBusType               *string = nil // nil==do not pass bus type
)

func getBusType() string {
	if testBusType == nil {
		return busDefaultValue
	} else {
		return *testBusType
	}
}

func getCreateVolumeRequest() *csi.CreateVolumeRequest {

	var volumeCapability *csi.VolumeCapability

	if testVolumeMode == corev1.PersistentVolumeFilesystem {
		volumeCapability = &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{},
		}
	} else {
		volumeCapability = &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Block{},
		}
	}

	parameters := map[string]string{}
	parameters[infraStorageClassNameParameter] = testInfraStorageClassName
	if testBusType != nil {
		parameters[busParameter] = *testBusType
	}

	return &csi.CreateVolumeRequest{
		Name: testVolumeName,
		CapacityRange: &csi.CapacityRange{
			RequiredBytes: testVolumeStorageSize,
		},
		VolumeCapabilities: []*csi.VolumeCapability{
			volumeCapability,
		},
		Parameters: parameters,
	}
}

func getDeleteVolumeRequest() *csi.DeleteVolumeRequest {
	return &csi.DeleteVolumeRequest{VolumeId: testVolumeName}
}

func getPublishVolumeRequest() *csi.ControllerPublishVolumeRequest {
	return &csi.ControllerPublishVolumeRequest{
		VolumeId: testVolumeName,
		NodeId:   testNodeID,
		VolumeContext: map[string]string{
			busParameter:    getBusType(),
			serialParameter: testDataVolumeUID,
		},
	}
}

func getUnpublishVolumeRequest() *csi.ControllerUnpublishVolumeRequest {
	return &csi.ControllerUnpublishVolumeRequest{
		VolumeId: testVolumeName,
		NodeId:   testNodeID,
	}
}

type ControllerClientMock struct {
	FailListVirtualMachines bool
	FailDeleteDataVolume    bool
	FailCreateDataVolume    bool
	FailAddVolumeToVM       bool
	FailRemoveVolumeFromVM  bool

	t *testing.T
}

func (c *ControllerClientMock) Ping(ctx context.Context) error {
	return errors.New("Not implemented")
}
func (c *ControllerClientMock) GetNamespace(ctx context.Context, name string) (*corev1.Namespace, error) {
	return nil, errors.New("Not implemented")
}
func (c *ControllerClientMock) ListNamespace(ctx context.Context) (*corev1.NamespaceList, error) {
	return nil, errors.New("Not implemented")
}
func (c *ControllerClientMock) GetStorageClass(ctx context.Context, name string) (*storagev1.StorageClass, error) {
	return nil, errors.New("Not implemented")
}
func (c *ControllerClientMock) ListVirtualMachines(namespace string) ([]kubevirtapiv1.VirtualMachineInstance, error) {
	if c.FailListVirtualMachines {
		return nil, errors.New("ListVirtualMachines failed")
	}

	return []kubevirtapiv1.VirtualMachineInstance{
		kubevirtapiv1.VirtualMachineInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name: testVmName,
			},
			Spec: kubevirtapiv1.VirtualMachineInstanceSpec{
				Domain: kubevirtapiv1.DomainSpec{
					Firmware: &kubevirtapiv1.Firmware{
						UUID: types.UID(testVmUID),
					},
				},
			},
		},
	}, nil
}
func (c *ControllerClientMock) DeleteDataVolume(namespace string, name string) error {
	if c.FailDeleteDataVolume {
		return errors.New("DeleteDataVolume failed")
	}

	// Test input
	assert.Equal(c.t, testVolumeName, name)

	return nil
}
func (c *ControllerClientMock) CreateDataVolume(namespace string, dataVolume *cdiv1alpha1.DataVolume) (*cdiv1alpha1.DataVolume, error) {
	if c.FailCreateDataVolume {
		return nil, errors.New("CreateDataVolume failed")
	}

	// Test input
	assert.Equal(c.t, testVolumeName, dataVolume.GetName())
	assert.Equal(c.t, testInfraStorageClassName, *dataVolume.Spec.PVC.StorageClassName)
	q, ok := dataVolume.Spec.PVC.Resources.Requests[corev1.ResourceStorage]
	assert.True(c.t, ok)
	assert.Equal(c.t, 0, q.CmpInt64(testVolumeStorageSize))
	assert.Equal(c.t, testVolumeMode, *dataVolume.Spec.PVC.VolumeMode)

	// Input OK. Now prepare result
	result := &cdiv1alpha1.DataVolume{}

	result.SetUID(types.UID(testDataVolumeUID))

	return result, nil
}
func (c *ControllerClientMock) GetDataVolume(namespace string, name string) (*cdiv1alpha1.DataVolume, error) {
	return nil, errors.New("Not implemented")
}
func (c *ControllerClientMock) ListDataVolumes(namespace string) ([]cdiv1alpha1.DataVolume, error) {
	return nil, errors.New("Not implemented")
}
func (c *ControllerClientMock) GetVMI(ctx context.Context, namespace string, name string) (*kubevirtapiv1.VirtualMachineInstance, error) {
	return nil, errors.New("Not implemented")
}
func (c *ControllerClientMock) AddVolumeToVM(namespace string, vmName string, addVolumeOptions *kubevirtapiv1.AddVolumeOptions) error {
	if c.FailAddVolumeToVM {
		return errors.New("AddVolumeToVM failed")
	}

	// Test input
	assert.Equal(c.t, testVmName, vmName)
	assert.Equal(c.t, hotplugDiskPrefix + testVolumeName, addVolumeOptions.Name)
	assert.Equal(c.t, testVolumeName, addVolumeOptions.VolumeSource.DataVolume.Name)
	assert.Equal(c.t, getBusType(), addVolumeOptions.Disk.DiskDevice.Disk.Bus)
	assert.Equal(c.t, testDataVolumeUID, addVolumeOptions.Disk.Serial)

	return nil
}
func (c *ControllerClientMock) RemoveVolumeFromVM(namespace string, vmName string, removeVolumeOptions *kubevirtapiv1.RemoveVolumeOptions) error {
	if c.FailRemoveVolumeFromVM {
		return errors.New("RemoveVolumeFromVM failed")
	}

	// Test input
	assert.Equal(c.t, testVmName, vmName)
	assert.Equal(c.t, hotplugDiskPrefix + testVolumeName, removeVolumeOptions.Name)

	return nil
}
