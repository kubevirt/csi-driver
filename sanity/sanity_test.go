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
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/kubernetes-csi/csi-test/v5/pkg/sanity"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	fakeclient "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/mount"
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

func TestMyDriver(t *testing.T) {
	RegisterTestingT(t)
	// Setup the full driver and its environment
	tempDir, err := ioutil.TempDir(os.TempDir(), "csi-sanity")
	Expect(err).ToNot(HaveOccurred())
	defer os.RemoveAll(tempDir)

	// Test labels
	infraClusterLabelsMap := map[string]string{}
	identityClientset := createIdentityClient()
	virtClient, deviceLister := createVirtClient()

	service.NewMounter = func() mount.Interface {
		return &fakeMounter{
			values: make([]mountArgs, 0),
		}
	}
	service.NewDeviceLister = func() service.DeviceLister {
		return deviceLister
	}

	service.NewFsMaker = func() service.FsMaker {
		return &fakeFsMaker{}
	}
	driver := service.NewKubevirtCSIDriver(virtClient,
		identityClientset,
		infraClusterNamespace,
		infraClusterLabelsMap,
		nodeID,
		true,
		true)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		endpoint := "unix://" + filepath.Join(tempDir, sanityEndpoint)
		driver.Run(endpoint)
	}()

	testConfig := sanity.NewTestConfig()
	// Set configuration options as needed
	testConfig.Address = filepath.Join(tempDir, sanityEndpoint)
	testConfig.StagingPath = filepath.Join(tempDir, "csi-staging")
	testConfig.TargetPath = filepath.Join(tempDir, "csi-mount")

	// Now call the test suite
	sanity.Test(t, testConfig)
}

func createIdentityClient() kubernetes.Interface {
	return fakeclient.NewSimpleClientset()
}

func createVirtClient() (kubevirt.Client, service.DeviceLister) {
	client := &fakeKubeVirtClient{
		dvMap:         make(map[string]*cdiv1.DataVolume),
		vmiMap:        make(map[string]*kubevirtv1.VirtualMachineInstance),
		hotpluggedMap: make(map[string]device),
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
}

func (k *fakeKubeVirtClient) Ping(ctx context.Context) error {
	return nil
}
func (k *fakeKubeVirtClient) ListVirtualMachines(namespace string) ([]kubevirtv1.VirtualMachineInstance, error) {
	var res []kubevirtv1.VirtualMachineInstance
	for _, v := range k.vmiMap {
		if v != nil {
			res = append(res, *v)
		}
	}
	return res, nil
}

func (k *fakeKubeVirtClient) DeleteDataVolume(namespace string, name string) error {
	key := getKey(namespace, name)
	delete(k.dvMap, key)
	return nil
}

func (k *fakeKubeVirtClient) GetDataVolume(namespace string, name string) (*cdiv1.DataVolume, error) {
	key := getKey(namespace, name)
	if k.dvMap[key] == nil {
		return nil, errors.NewNotFound(cdiv1.Resource("DataVolume"), name)
	}
	return k.dvMap[key], nil
}

func (k *fakeKubeVirtClient) CreateDataVolume(namespace string, dataVolume *cdiv1.DataVolume) (*cdiv1.DataVolume, error) {
	if dataVolume == nil {
		return nil, fmt.Errorf("Nil datavolume passed")
	}
	key := getKey(namespace, dataVolume.Name)
	dataVolume.SetUID(types.UID(uuid.NewString()))
	k.dvMap[key] = dataVolume
	return dataVolume, nil
}
func (k *fakeKubeVirtClient) AddVolumeToVM(namespace string, vmName string, hotPlugRequest *kubevirtv1.AddVolumeOptions) error {
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

func (k *fakeKubeVirtClient) RemoveVolumeFromVM(namespace string, vmName string, hotPlugRequest *kubevirtv1.RemoveVolumeOptions) error {
	vmKey := getKey(namespace, vmName)
	if k.vmiMap[vmKey] == nil {
		return fmt.Errorf("VM %s/%s not found", namespace, vmName)
	}
	delete(k.hotpluggedMap, hotPlugRequest.Name)
	return nil
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
	return nil, fmt.Errorf("Called GetMountRefs somewhere")
}

type fakeFsMaker struct{}

func (fm *fakeFsMaker) Make(device string, fsType string) error {
	return nil
}
