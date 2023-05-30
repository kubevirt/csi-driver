package service

import (
	"errors"
	"testing"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubevirtv1 "kubevirt.io/api/core/v1"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	"kubevirt.io/csi-driver/pkg/util"
)

func TestCreateVolumeDefaultStorageClass_Success(t *testing.T) {

	origStorageClass := testInfraStorageClassName
	testInfraStorageClassName = ""
	storageClassEnforcement = util.StorageClassEnforcement{
		AllowAll:     true,
		AllowDefault: true,
	}
	defer func() { testInfraStorageClassName = origStorageClass }()

	client := &ControllerClientMock{t: t}
	controller := ControllerService{client, testInfraNamespace, testInfraLabels, storageClassEnforcement}

	response, err := controller.CreateVolume(context.TODO(), getCreateVolumeRequest())
	assert.Nil(t, err)

	assert.Equal(t, testVolumeName, response.GetVolume().GetVolumeId())
	assert.Equal(t, testDataVolumeUID, response.GetVolume().VolumeContext[serialParameter])
	assert.Equal(t, string(getBusType()), response.GetVolume().VolumeContext[busParameter])
	assert.Equal(t, testVolumeStorageSize, response.GetVolume().GetCapacityBytes())
}

func TestCreateVolume_Success(t *testing.T) {
	client := &ControllerClientMock{t: t}
	controller := ControllerService{client, testInfraNamespace, testInfraLabels, storageClassEnforcement}

	response, err := controller.CreateVolume(context.TODO(), getCreateVolumeRequest())
	assert.Nil(t, err)

	assert.Equal(t, testVolumeName, response.GetVolume().GetVolumeId())
	assert.Equal(t, testDataVolumeUID, response.GetVolume().VolumeContext[serialParameter])
	assert.Equal(t, string(getBusType()), response.GetVolume().VolumeContext[busParameter])
	assert.Equal(t, testVolumeStorageSize, response.GetVolume().GetCapacityBytes())
}

func TestCreateVolume_CreateDataVolumeFail(t *testing.T) {
	client := &ControllerClientMock{t: t, FailCreateDataVolume: true}
	controller := ControllerService{client, testInfraNamespace, testInfraLabels, storageClassEnforcement}

	_, err := controller.CreateVolume(context.TODO(), getCreateVolumeRequest())
	assert.NotNil(t, err)
}

func TestCreateVolume_CustomBus(t *testing.T) {
	client := &ControllerClientMock{t: t}
	controller := ControllerService{client, testInfraNamespace, testInfraLabels, storageClassEnforcement}

	busTypeLocal := kubevirtv1.DiskBusVirtio
	testBusType = &busTypeLocal

	response, err := controller.CreateVolume(context.TODO(), getCreateVolumeRequest())
	assert.Nil(t, err)

	assert.Equal(t, response.GetVolume().GetVolumeContext()[busParameter], string(*testBusType))
}

func TestCreateVolume_NotAllowedStorageClass(t *testing.T) {
	client := &ControllerClientMock{t: t}
	allowedStorageClasses = []string{"allowedClass"}
	allowAllStorageClasses = false
	storageClassEnforcement = util.StorageClassEnforcement{
		AllowList:    []string{"allowedClass"},
		AllowAll:     false,
		AllowDefault: true,
	}
	controller := ControllerService{client, testInfraNamespace, testInfraLabels, storageClassEnforcement}

	request := getCreateVolumeRequest()
	request.Parameters[infraStorageClassNameParameter] = "notAllowedClass"

	_, err := controller.CreateVolume(context.TODO(), request)
	assert.Equal(t, unallowedStorageClass, err)
}

func TestDeleteVolume_Success(t *testing.T) {
	client := &ControllerClientMock{t: t}
	controller := ControllerService{client, testInfraNamespace, testInfraLabels, storageClassEnforcement}

	_, err := controller.DeleteVolume(context.TODO(), getDeleteVolumeRequest())
	assert.Nil(t, err)
}

func TestDeleteVolume_Fail(t *testing.T) {
	client := &ControllerClientMock{t: t, FailDeleteDataVolume: true}
	controller := ControllerService{client, testInfraNamespace, testInfraLabels, storageClassEnforcement}

	_, err := controller.DeleteVolume(context.TODO(), getDeleteVolumeRequest())
	assert.NotNil(t, err)
}

func TestPublishVolume_Success(t *testing.T) {
	client := &ControllerClientMock{t: t}
	controller := ControllerService{client, testInfraNamespace, testInfraLabels, storageClassEnforcement}

	_, err := controller.ControllerPublishVolume(context.TODO(), getPublishVolumeRequest()) // AddVolumeToVM tests the hotplug request
	assert.Nil(t, err)
}

func TestUnpublishVolume_Success(t *testing.T) {
	client := &ControllerClientMock{t: t}
	client.virtualMachineStatus.VolumeStatus = append(client.virtualMachineStatus.VolumeStatus, kubevirtv1.VolumeStatus{
		Name:          testVolumeName,
		HotplugVolume: &kubevirtv1.HotplugVolumeStatus{},
	})
	controller := ControllerService{client, testInfraNamespace, testInfraLabels, storageClassEnforcement}

	_, err := controller.ControllerUnpublishVolume(context.TODO(), getUnpublishVolumeRequest())
	assert.Nil(t, err)
}

func TestUnpublishVolume_Nothotplugged(t *testing.T) {
	client := &ControllerClientMock{t: t}
	controller := ControllerService{client, testInfraNamespace, testInfraLabels, storageClassEnforcement}

	_, err := controller.ControllerUnpublishVolume(context.TODO(), getUnpublishVolumeRequest())
	assert.Nil(t, err)
}

//
// The rest of the file is code used by the tests and tests infrastructure
//

var (
	testVolumeName                                = "pvc-3d8be521-6e4b-4a87-add4-1961bf62f4ea"
	testInfraStorageClassName                     = "infra-storage"
	testVolumeStorageSize     int64               = 1024 * 1024 * 1024 * 3
	testInfraNamespace                            = "tenant-cluster-2"
	testNodeID                                    = "6FC9C805-B3A0-570B-9D1B-3B8B9CFC9FB7"
	testVMName                                    = "test-vm"
	testVMUID                                     = "6fc9c805-b3a0-570b-9d1b-3b8b9cfc9fb7"
	testDataVolumeUID                             = "2d0111d5-494f-4731-8f67-122b27d3c366"
	testVolumeMode                                = corev1.PersistentVolumeFilesystem
	testBusType               *kubevirtv1.DiskBus = nil // nil==do not pass bus type
	testInfraLabels                               = map[string]string{"infra-label-name": "infra-label-value"}
	allowedStorageClasses                         = []string{}
	allowAllStorageClasses                        = true
	storageClassEnforcement                       = util.StorageClassEnforcement{
		AllowAll:     true,
		AllowDefault: true,
	}
)

func getBusType() kubevirtv1.DiskBus {
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
	if testInfraStorageClassName != "" {
		parameters[infraStorageClassNameParameter] = testInfraStorageClassName
	}
	if testBusType != nil {
		parameters[busParameter] = string(*testBusType)
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
			busParameter:    string(getBusType()),
			serialParameter: testDataVolumeUID,
		},
		VolumeCapability: &csi.VolumeCapability{},
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
	virtualMachineStatus    kubevirtv1.VirtualMachineInstanceStatus

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
func (c *ControllerClientMock) ListVirtualMachines(namespace string) ([]kubevirtv1.VirtualMachineInstance, error) {
	if c.FailListVirtualMachines {
		return nil, errors.New("ListVirtualMachines failed")
	}

	return []kubevirtv1.VirtualMachineInstance{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testVMName,
				Namespace: namespace,
			},
			Spec: kubevirtv1.VirtualMachineInstanceSpec{
				Domain: kubevirtv1.DomainSpec{
					Firmware: &kubevirtv1.Firmware{
						UUID: types.UID(testVMUID),
					},
				},
			},
		},
	}, nil
}

func (c *ControllerClientMock) GetVirtualMachine(namespace, name string) (*kubevirtv1.VirtualMachineInstance, error) {
	if c.FailListVirtualMachines {
		return nil, errors.New("ListVirtualMachines failed")
	}

	return &kubevirtv1.VirtualMachineInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: kubevirtv1.VirtualMachineInstanceSpec{
			Domain: kubevirtv1.DomainSpec{
				Firmware: &kubevirtv1.Firmware{
					UUID: types.UID(testVMUID),
				},
			},
		},
		Status: c.virtualMachineStatus,
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
func (c *ControllerClientMock) CreateDataVolume(namespace string, dataVolume *cdiv1.DataVolume) (*cdiv1.DataVolume, error) {
	if c.FailCreateDataVolume {
		return nil, errors.New("CreateDataVolume failed")
	}

	// Test input
	assert.Equal(c.t, testVolumeName, dataVolume.GetName())
	if testInfraStorageClassName != "" {
		assert.Equal(c.t, testInfraStorageClassName, *dataVolume.Spec.Storage.StorageClassName)
	} else {
		assert.Nil(c.t, dataVolume.Spec.Storage.StorageClassName)
	}
	q, ok := dataVolume.Spec.Storage.Resources.Requests[corev1.ResourceStorage]
	assert.True(c.t, ok)
	assert.Equal(c.t, 0, q.CmpInt64(testVolumeStorageSize))
	assert.Equal(c.t, testInfraLabels, dataVolume.Labels)

	// Input OK. Now prepare result
	result := &cdiv1.DataVolume{}

	result.SetUID(types.UID(testDataVolumeUID))

	return result, nil
}
func (c *ControllerClientMock) GetDataVolume(namespace string, name string) (*cdiv1.DataVolume, error) {
	return nil, k8serrors.NewNotFound(cdiv1.Resource("DataVolume"), name)
}
func (c *ControllerClientMock) ListDataVolumes(namespace string) ([]cdiv1.DataVolume, error) {
	return nil, errors.New("Not implemented")
}
func (c *ControllerClientMock) GetVMI(ctx context.Context, namespace string, name string) (*kubevirtv1.VirtualMachineInstance, error) {
	return nil, errors.New("Not implemented")
}
func (c *ControllerClientMock) AddVolumeToVM(namespace string, vmName string, addVolumeOptions *kubevirtv1.AddVolumeOptions) error {
	if c.FailAddVolumeToVM {
		return errors.New("AddVolumeToVM failed")
	}

	// Test input
	assert.Equal(c.t, testVMName, vmName)
	assert.Equal(c.t, testVolumeName, addVolumeOptions.Name)
	assert.Equal(c.t, testVolumeName, addVolumeOptions.VolumeSource.DataVolume.Name)
	assert.Equal(c.t, getBusType(), addVolumeOptions.Disk.DiskDevice.Disk.Bus)
	assert.Equal(c.t, testDataVolumeUID, addVolumeOptions.Disk.Serial)

	return nil
}
func (c *ControllerClientMock) RemoveVolumeFromVM(namespace string, vmName string, removeVolumeOptions *kubevirtv1.RemoveVolumeOptions) error {
	if c.FailRemoveVolumeFromVM {
		return errors.New("RemoveVolumeFromVM failed")
	}

	// Test input
	assert.Equal(c.t, testVMName, vmName)
	assert.Equal(c.t, testVolumeName, removeVolumeOptions.Name)

	return nil
}

func (c *ControllerClientMock) EnsureVolumeAvailable(namespace, vmName, volumeName string, timeout time.Duration) error {
	return nil
}
