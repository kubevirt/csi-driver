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

func createVirtClient() (kubevirt.Client, service.DeviceLister) {
	client := &fakeKubeVirtClient{
		dvMap:         make(map[string]*cdiv1.DataVolume),
		vmiMap:        make(map[string]*kubevirtv1.VirtualMachineInstance),
		hotpluggedMap: make(map[string]device),
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
	return client, client
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
	hotpluggedMap map[string]device
	snapshotMap   map[string]*snapshotv1.VolumeSnapshot
}

func (k *fakeKubeVirtClient) Ping(ctx context.Context) error {
	return nil
}
func (k *fakeKubeVirtClient) ListVirtualMachines(_ context.Context, namespace string) ([]kubevirtv1.VirtualMachineInstance, error) {
	var res []kubevirtv1.VirtualMachineInstance
	for _, v := range k.vmiMap {
		if v != nil {
			res = append(res, *v)
		}
	}
	return res, nil
}

func (k *fakeKubeVirtClient) GetVirtualMachine(_ context.Context, namespace, vmName string) (*kubevirtv1.VirtualMachineInstance, error) {
	vmKey := getKey(namespace, vmName)
	return k.vmiMap[vmKey], nil
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

func (k *fakeKubeVirtClient) EnsureVolumeAvailable(_ context.Context, namespace, vmName, volumeName string, timeout time.Duration) error {
	return nil
}

func (k *fakeKubeVirtClient) EnsureVolumeRemoved(_ context.Context, namespace, vmName, volumeName string, timeout time.Duration) error {
	return nil
}

func (k *fakeKubeVirtClient) EnsureSnapshotReady(_ context.Context, namespace, name string, timeout time.Duration) error {
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

func (k *fakeKubeVirtClient) List() ([]byte, error) {
	ds := make([]device, 0)
	for _, value := range k.hotpluggedMap {
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
	values []mountArgs
}

func (m *fakeMounter) Mount(source string, target string, fstype string, options []string) error {
	newArgs := mountArgs{
		source:  source,
		target:  target,
		fstype:  fstype,
		options: options,
	}
	exists := false
	for _, args := range m.values {
		if reflect.DeepEqual(args, newArgs) {
			exists = true
		}
	}
	if !exists {
		m.values = append(m.values, newArgs)
	}
	return nil
}

func (m *fakeMounter) MountSensitive(source string, target string, fstype string, options []string, sensitiveOptions []string) error {
	return m.Mount(source, target, fstype, options)
}

func (m *fakeMounter) Unmount(target string) error {
	existingValues := make([]mountArgs, 0)
	for _, args := range m.values {
		if args.target != target {
			existingValues = append(existingValues, args)
		} else {
			err := os.RemoveAll(target)
			Expect(err).ToNot(HaveOccurred())
		}
	}
	m.values = existingValues
	return nil
}

func (m *fakeMounter) List() ([]mount.MountPoint, error) {
	res := make([]mount.MountPoint, 0)
	for _, args := range m.values {
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

type fakeFsMaker struct{}

func (fm *fakeFsMaker) Make(device string, fsType string) error {
	return nil
}
