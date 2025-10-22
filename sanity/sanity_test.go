/*
Copyright 2022 The csi driver Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package sanity

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/google/uuid"
	"github.com/kubernetes-csi/csi-test/v5/pkg/sanity"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	fakeclient "k8s.io/client-go/kubernetes/fake"
	mount "k8s.io/mount-utils"
	"k8s.io/utils/ptr"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/csi-driver/pkg/kubevirt"
	"kubevirt.io/csi-driver/pkg/mounter"
	"kubevirt.io/csi-driver/pkg/service"

	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

const (
	sanityEndpoint = "sanity.sock"
	// Test namespace for infra cluster
	infraClusterNamespace = "infra-namespace"
	nodeID                = "testnode"
)

var _ = Describe("KubeVirt CSI-Driver", func() {
	Describe("Should complete sanity", func() {
		sanity.GinkgoTest(&testConfig)
	})
})

func createIdentityClient() kubernetes.Interface {
	return fakeclient.NewSimpleClientset()
}

func createVirtClient(hotpluggedMap map[string]device) kubevirt.Client {
	client := &fakeKubeVirtClient{
		dvMap:         make(map[string]*cdiv1.DataVolume),
		vmMap:         make(map[string]*kubevirtv1.VirtualMachine),
		vmiMap:        make(map[string]*kubevirtv1.VirtualMachineInstance),
		hotpluggedMap: hotpluggedMap,
		snapshotMap:   make(map[string]*snapshotv1.VolumeSnapshot),
	}
	key := getKey(infraClusterNamespace, nodeID)
	client.vmiMap[key] = &kubevirtv1.VirtualMachineInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      nodeID,
			Namespace: infraClusterNamespace,
		},
		Spec: kubevirtv1.VirtualMachineInstanceSpec{
			Domain: kubevirtv1.DomainSpec{
				Firmware: &kubevirtv1.Firmware{
					UUID: nodeID,
				},
			},
		},
	}
	client.vmMap[key] = &kubevirtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      nodeID,
			Namespace: infraClusterNamespace,
		},
		Spec: kubevirtv1.VirtualMachineSpec{
			Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
				Spec: kubevirtv1.VirtualMachineInstanceSpec{
					Volumes: make([]kubevirtv1.Volume, 0),
				},
			},
		},
	}
	return client
}

type devices struct {
	BlockDevices []device `json:"blockdevices"`
}

type device struct {
	SerialID string `json:"serial"`
	Path     string `json:"path,omitempty"`
	Name     string `json:"name"`
	Fstype   string `json:"fstype"`
}

type fakeKubeVirtClient struct {
	dvMap         map[string]*cdiv1.DataVolume
	vmiMap        map[string]*kubevirtv1.VirtualMachineInstance
	vmMap         map[string]*kubevirtv1.VirtualMachine
	hotpluggedMap map[string]device
	snapshotMap   map[string]*snapshotv1.VolumeSnapshot
}

func (k *fakeKubeVirtClient) Ping(ctx context.Context) error {
	return nil
}
func (k *fakeKubeVirtClient) ListVirtualMachines(_ context.Context, namespace string) ([]*kubevirtv1.VirtualMachineInstance, error) {
	var res []*kubevirtv1.VirtualMachineInstance
	for _, v := range k.vmiMap {
		if v != nil {
			res = append(res, v)
		}
	}
	return res, nil
}

func (k *fakeKubeVirtClient) GetVirtualMachine(_ context.Context, namespace, vmName string) (*kubevirtv1.VirtualMachineInstance, error) {
	vmKey := getKey(namespace, vmName)
	return k.vmiMap[vmKey], nil
}

func (k *fakeKubeVirtClient) GetWorkloadManagingVirtualMachine(_ context.Context, namespace, vmName string) (*kubevirtv1.VirtualMachine, error) {
	vmKey := getKey(namespace, vmName)
	if k.vmMap[vmKey] == nil {
		return nil, errors.NewNotFound(corev1.Resource("vm"), vmName)
	}
	return k.vmMap[vmKey], nil
}

func (k *fakeKubeVirtClient) DeleteDataVolume(_ context.Context, namespace string, name string) error {
	key := getKey(namespace, name)
	delete(k.dvMap, key)
	return nil
}

func (k *fakeKubeVirtClient) GetDataVolume(_ context.Context, namespace string, name string) (*cdiv1.DataVolume, error) {
	key := getKey(namespace, name)
	if k.dvMap[key] == nil {
		return nil, errors.NewNotFound(cdiv1.Resource("DataVolume"), name)
	}
	return k.dvMap[key], nil
}

func (k *fakeKubeVirtClient) GetPersistentVolumeClaim(_ context.Context, namespace string, claimName string) (*corev1.PersistentVolumeClaim, error) {
	// Figure out correct impl. for sanity
	return nil, nil
}

func (k *fakeKubeVirtClient) ExpandPersistentVolumeClaim(_ context.Context, namespace string, claimName string, size int64) error {
	// Figure out correct impl. for sanity
	return nil
}

func (k *fakeKubeVirtClient) CreateDataVolume(_ context.Context, namespace string, dataVolume *cdiv1.DataVolume) (*cdiv1.DataVolume, error) {
	if dataVolume == nil {
		return nil, fmt.Errorf("Nil datavolume passed")
	}
	key := getKey(namespace, dataVolume.Name)
	dataVolume.SetUID(types.UID(uuid.NewString()))
	k.dvMap[key] = dataVolume
	return dataVolume, nil
}
func (k *fakeKubeVirtClient) AddVolumeToVM(_ context.Context, namespace string, vmName string, hotPlugRequest *kubevirtv1.AddVolumeOptions) error {
	vmKey := getKey(namespace, vmName)
	if k.vmiMap[vmKey] == nil {
		return fmt.Errorf("VM %s/%s not found", namespace, vmName)
	}
	k.hotpluggedMap[hotPlugRequest.Name] = device{
		SerialID: hotPlugRequest.Disk.Serial,
		Name:     hotPlugRequest.Name,
		Fstype:   "ext4",
	}
	return nil
}

func (k *fakeKubeVirtClient) RemoveVolumeFromVM(_ context.Context, namespace string, vmName string, hotPlugRequest *kubevirtv1.RemoveVolumeOptions) error {
	vmKey := getKey(namespace, vmName)
	if k.vmiMap[vmKey] == nil {
		return fmt.Errorf("VM %s/%s not found", namespace, vmName)
	}
	delete(k.hotpluggedMap, hotPlugRequest.Name)
	return nil
}

func (k *fakeKubeVirtClient) RemoveVolumeFromVMI(_ context.Context, namespace string, vmName string, hotPlugRequest *kubevirtv1.RemoveVolumeOptions) error {
	return nil
}

func (k *fakeKubeVirtClient) EnsureVolumeAvailable(_ context.Context, namespace, vmName, volumeName string, timeout time.Duration) error {
	return nil
}

func (c *fakeKubeVirtClient) EnsureVolumeAvailableVM(_ context.Context, namespace, vmName, volName string) (bool, error) {
	return false, nil
}

func (c *fakeKubeVirtClient) EnsureVolumeRemovedVM(_ context.Context, namespace, vmName, volName string) (bool, error) {
	return false, nil
}

func (k *fakeKubeVirtClient) EnsureVolumeRemoved(_ context.Context, namespace, vmName, volumeName string, timeout time.Duration) error {
	return nil
}

func (k *fakeKubeVirtClient) EnsureSnapshotReady(_ context.Context, namespace, name string, timeout time.Duration) error {
	return nil
}

func (k *fakeKubeVirtClient) EnsureControllerResize(_ context.Context, namespace, claimName string, timeout time.Duration) error {
	return nil
}

func (k *fakeKubeVirtClient) CreateVolumeSnapshot(_ context.Context, namespace, name, volumeName, snapclassName string) (*snapshotv1.VolumeSnapshot, error) {
	snapshot := &snapshotv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: snapshotv1.VolumeSnapshotSpec{
			Source: snapshotv1.VolumeSnapshotSource{
				PersistentVolumeClaimName: &volumeName,
			},
			VolumeSnapshotClassName: &snapclassName,
		},
		Status: &snapshotv1.VolumeSnapshotStatus{
			ReadyToUse:                     ptr.To[bool](true),
			RestoreSize:                    ptr.To[resource.Quantity](resource.MustParse("1Gi")),
			BoundVolumeSnapshotContentName: ptr.To[string]("snapcontent"),
		},
	}
	key := getKey(namespace, name)
	snapshot.SetUID(types.UID(uuid.NewString()))
	k.snapshotMap[key] = snapshot
	return snapshot, nil
}

func (k *fakeKubeVirtClient) GetVolumeSnapshot(_ context.Context, namespace, name string) (*snapshotv1.VolumeSnapshot, error) {
	snapKey := getKey(namespace, name)
	if k.snapshotMap[snapKey] == nil {
		return nil, errors.NewNotFound(snapshotv1.Resource("VolumeSnapshot"), name)
	}
	return k.snapshotMap[snapKey], nil
}

func (k *fakeKubeVirtClient) DeleteVolumeSnapshot(_ context.Context, namespace, name string) error {
	snapKey := getKey(namespace, name)
	delete(k.snapshotMap, snapKey)
	return nil
}

func (k *fakeKubeVirtClient) ListVolumeSnapshots(_ context.Context, namespace string) (*snapshotv1.VolumeSnapshotList, error) {
	res := snapshotv1.VolumeSnapshotList{}
	for _, s := range k.snapshotMap {
		if s != nil {
			res.Items = append(res.Items, *s)
		}
	}
	return &res, nil
}

type fakeDeviceLister struct {
	hotpluggedMap map[string]device
}

func (f *fakeDeviceLister) List() ([]byte, error) {
	ds := make([]device, 0)
	for _, value := range f.hotpluggedMap {
		ds = append(ds, value)
	}
	d := devices{
		BlockDevices: ds,
	}
	return json.Marshal(d)
}

func getKey(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}

type mountArgs struct {
	source  string
	target  string
	fstype  string
	options []string
}

type fakeMounter struct {
	values *[]mountArgs
}

func (m *fakeMounter) Mount(source string, target string, fstype string, options []string) error {
	newArgs := mountArgs{
		source:  source,
		target:  target,
		fstype:  fstype,
		options: options,
	}
	exists := false
	for _, args := range *m.values {
		if reflect.DeepEqual(args, newArgs) {
			exists = true
		}
	}
	if !exists {
		*m.values = append(*m.values, newArgs)
	}
	return nil
}

func (m *fakeMounter) MountSensitive(source string, target string, fstype string, options []string, sensitiveOptions []string) error {
	return m.Mount(source, target, fstype, options)
}

func (m *fakeMounter) Unmount(target string) error {
	existingValues := make([]mountArgs, 0)
	for _, args := range *m.values {
		if args.target != target {
			existingValues = append(existingValues, args)
		} else {
			err := os.RemoveAll(target)
			Expect(err).ToNot(HaveOccurred())
		}
	}
	*m.values = existingValues
	return nil
}

func (m *fakeMounter) List() ([]mount.MountPoint, error) {
	res := make([]mount.MountPoint, 0)
	for _, args := range *m.values {
		res = append(res, mount.MountPoint{
			Device: args.source,
			Path:   args.target,
			Type:   args.fstype,
		})
	}
	return res, nil
}

func (m *fakeMounter) IsLikelyNotMountPoint(file string) (bool, error) {
	return true, nil
}

func (m *fakeMounter) GetMountRefs(pathname string) ([]string, error) {
	panic("shouldn't have called GetMountRefs")
}

func (m *fakeMounter) CanSafelySkipMountPointCheck() bool {
	panic("shouldn't have called CanSafelySkipMountPointCheck")
}

func (m *fakeMounter) IsMountPoint(file string) (bool, error) {
	panic("shouldn't have called IsMountPoint")
}

func (m *fakeMounter) MountSensitiveWithoutSystemd(source string, target string, fstype string, options []string, sensitiveOptions []string) error {
	panic("shouldn't have called MountSensitiveWithoutSystemd")
}

func (m *fakeMounter) MountSensitiveWithoutSystemdWithMountFlags(source string, target string, fstype string, options []string, sensitiveOptions []string, mountFlags []string) error {
	panic("shouldn't have called MountSensitiveWithoutSystemdWithMountFlags")
}

func (m *fakeMounter) IsBlockDevice(fullPath string) (bool, error) {
	return false, nil
}

func (m *fakeMounter) GetBlockSizeBytes(devicePath string) (int64, error) {
	return 0, nil
}

func (m *fakeMounter) GetVolumeStats(volumePath string) (mounter.VolumeStats, error) {
	return mounter.VolumeStats{
		AvailableBytes:  0,
		TotalBytes:      0,
		UsedBytes:       0,
		AvailableInodes: 0,
		TotalInodes:     0,
		UsedInodes:      0,
	}, nil
}

func (m *fakeMounter) PathExists(path string) (bool, error) {
	for _, val := range *m.values {
		if val.target == path {
			return true, nil
		}
	}

	return false, nil
}

type fakeFsMaker struct{}

func (fm *fakeFsMaker) Make(device string, fsType string) error {
	return nil
}

type fakeResizer struct {
	resizedMap map[string]struct{}
}

func (r *fakeResizer) Resize(devicePath, deviceMountPath string) (bool, error) {
	if r.resizedMap == nil {
		r.resizedMap = map[string]struct{}{}
	}
	key := fmt.Sprintf("%s:%s", devicePath, deviceMountPath)
	r.resizedMap[key] = struct{}{}

	return true, nil
}

func (r *fakeResizer) NeedResize(devicePath string, deviceMountPath string) (bool, error) {
	return true, nil
}

type fakeDevicePathGetter struct {
	mountArgs *[]mountArgs
}

func (d *fakeDevicePathGetter) Get(mountPath string) (string, error) {
	for _, args := range *d.mountArgs {
		if args.target == mountPath {
			return args.source, nil
		}
	}

	return "", service.ErrMountDeviceNotFound
}
