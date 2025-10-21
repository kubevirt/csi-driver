package service

import (
	"errors"
	"fmt"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	kubevirtv1 "kubevirt.io/api/core/v1"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"

	client "kubevirt.io/csi-driver/pkg/kubevirt"
	"kubevirt.io/csi-driver/pkg/util"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("StorageClass", func() {
	It("should successfully create a default storage class datavolume", func() {
		origStorageClass := testInfraStorageClassName
		testInfraStorageClassName = ""
		storageClassEnforcement = util.StorageClassEnforcement{
			AllowAll:     true,
			AllowDefault: true,
		}
		defer func() { testInfraStorageClassName = origStorageClass }()

		client := &ControllerClientMock{}
		controller := ControllerService{
			virtClient:              client,
			infraClusterNamespace:   testInfraNamespace,
			infraClusterLabels:      testInfraLabels,
			storageClassEnforcement: storageClassEnforcement,
		}

		response, err := controller.CreateVolume(context.TODO(), getCreateVolumeRequest(getVolumeCapability(corev1.PersistentVolumeFilesystem, csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER)))
		Expect(err).ToNot(HaveOccurred())
		Expect(testVolumeName).To(Equal(response.GetVolume().GetVolumeId()))
		Expect(testDataVolumeUID).To(Equal(response.GetVolume().VolumeContext[serialParameter]))
		Expect(string(getBusType())).To(Equal(response.GetVolume().VolumeContext[busParameter]))
		Expect(testVolumeStorageSize).To(Equal(response.GetVolume().GetCapacityBytes()))
	})
})

var _ = Describe("CreateVolume", func() {
	AfterEach(func() {
		storageClassEnforcement = util.StorageClassEnforcement{
			AllowAll:     true,
			AllowDefault: true,
		}
	})

	DescribeTable("should successfully create a volume", func(cap *csi.VolumeCapability, expectedAC *corev1.PersistentVolumeAccessMode) {
		client := &ControllerClientMock{}
		controller := ControllerService{
			virtClient:              client,
			infraClusterNamespace:   testInfraNamespace,
			infraClusterLabels:      testInfraLabels,
			storageClassEnforcement: storageClassEnforcement,
		}

		request := getCreateVolumeRequest(cap)
		response, err := controller.CreateVolume(context.TODO(), request)
		Expect(err).ToNot(HaveOccurred())

		Expect(response.GetVolume().GetVolumeId()).To(Equal(testVolumeName))
		Expect(response.GetVolume().VolumeContext[serialParameter]).To(Equal(testDataVolumeUID))
		Expect(response.GetVolume().VolumeContext[busParameter]).To(Equal(string(getBusType())))
		Expect(response.GetVolume().GetCapacityBytes()).To(Equal(testVolumeStorageSize))

		dv, err := client.GetDataVolume(context.TODO(), testInfraNamespace, request.Name)
		Expect(err).ToNot(HaveOccurred())
		Expect(dv).ToNot(BeNil())

		if expectedAC != nil {
			Expect(dv.Spec.Storage).ToNot(BeNil())
			Expect(dv.Spec.Storage.AccessModes).ToNot(BeEmpty())
			Expect(dv.Spec.Storage.AccessModes[0]).To(Equal(*expectedAC))
		} else if dv.Spec.Storage != nil {
			Expect(dv.Spec.Storage.AccessModes).To(BeEmpty())
		}
	},
		Entry("volume mode = block; [RWX]", getVolumeCapability(corev1.PersistentVolumeBlock, csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER), ptr.To(corev1.ReadWriteMany)),
		Entry("volume mode = block; [RWO]", getVolumeCapability(corev1.PersistentVolumeBlock, csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER), nil),
		Entry("volume mode = filesystem; [RWO]", getVolumeCapability(corev1.PersistentVolumeFilesystem, csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER), nil),
	)

	It("should reject create volume request for FS & RWX", func() {
		client := &ControllerClientMock{}
		controller := ControllerService{
			virtClient:              client,
			infraClusterNamespace:   testInfraNamespace,
			infraClusterLabels:      testInfraLabels,
			storageClassEnforcement: storageClassEnforcement,
		}

		response, err := controller.CreateVolume(context.TODO(), getCreateVolumeRequest(getVolumeCapability(corev1.PersistentVolumeFilesystem, csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER)))
		Expect(err).To(MatchError(ContainSubstring("non-block volume with RWX access mode is not supported")))
		Expect(response).To(BeNil())
	})

	It("should propagate error from CreateVolume", func() {
		client := &ControllerClientMock{FailCreateDataVolume: true}
		controller := ControllerService{
			virtClient:              client,
			infraClusterNamespace:   testInfraNamespace,
			infraClusterLabels:      testInfraLabels,
			storageClassEnforcement: storageClassEnforcement,
		}

		_, err := controller.CreateVolume(context.TODO(), getCreateVolumeRequest(getVolumeCapability(corev1.PersistentVolumeFilesystem, csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER)))
		Expect(err).To(HaveOccurred())
	})

	It("should accept custom bus type", func() {
		client := &ControllerClientMock{}
		controller := ControllerService{
			virtClient:              client,
			infraClusterNamespace:   testInfraNamespace,
			infraClusterLabels:      testInfraLabels,
			storageClassEnforcement: storageClassEnforcement,
		}

		busTypeLocal := kubevirtv1.DiskBusVirtio
		testBusType = &busTypeLocal

		response, err := controller.CreateVolume(context.TODO(), getCreateVolumeRequest(getVolumeCapability(corev1.PersistentVolumeFilesystem, csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER)))
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetVolume().GetVolumeContext()[busParameter]).To(Equal(string(busTypeLocal)))
	})

	It("should not allow storage class not in the allow list", func() {
		cli := &ControllerClientMock{}
		storageClassEnforcement = util.StorageClassEnforcement{
			AllowList:    []string{"allowedClass"},
			AllowAll:     false,
			AllowDefault: true,
		}
		controller := ControllerService{
			virtClient:              cli,
			infraClusterNamespace:   testInfraNamespace,
			infraClusterLabels:      testInfraLabels,
			storageClassEnforcement: storageClassEnforcement,
		}

		request := getCreateVolumeRequest(getVolumeCapability(corev1.PersistentVolumeFilesystem, csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER))
		request.Parameters[client.InfraStorageClassNameParameter] = "notAllowedClass"

		_, err := controller.CreateVolume(context.TODO(), request)
		Expect(err).To(HaveOccurred())
		Expect(err).To(Equal(unallowedStorageClass))
	})

	It("should create a volume with a snapshot datasource", func() {
		client := &ControllerClientMock{}
		client.snapshots = make(map[string]*snapshotv1.VolumeSnapshot)
		client.snapshots[getKey(testInfraNamespace, "snapshot-1")] = &snapshotv1.VolumeSnapshot{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "snapshot-1",
				Namespace: testInfraNamespace,
			},
		}
		controller := ControllerService{
			virtClient:              client,
			infraClusterNamespace:   testInfraNamespace,
			infraClusterLabels:      testInfraLabels,
			storageClassEnforcement: storageClassEnforcement,
		}

		request := getCreateVolumeRequest(getVolumeCapability(corev1.PersistentVolumeFilesystem, csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER))
		request.VolumeContentSource = &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Snapshot{
				Snapshot: &csi.VolumeContentSource_SnapshotSource{
					SnapshotId: "snapshot-1",
				},
			},
		}

		_, err := controller.CreateVolume(context.TODO(), request)
		Expect(err).ToNot(HaveOccurred())
	})

	It("should fail to create a volume with a snapshot datasource, if snapshot not found", func() {
		client := &ControllerClientMock{}
		controller := ControllerService{
			virtClient:              client,
			infraClusterNamespace:   testInfraNamespace,
			infraClusterLabels:      testInfraLabels,
			storageClassEnforcement: storageClassEnforcement,
		}

		request := getCreateVolumeRequest(getVolumeCapability(corev1.PersistentVolumeFilesystem, csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER))
		request.VolumeContentSource = &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Snapshot{
				Snapshot: &csi.VolumeContentSource_SnapshotSource{
					SnapshotId: "snapshot-1",
				},
			},
		}

		_, err := controller.CreateVolume(context.TODO(), request)
		Expect(err).To(HaveOccurred())
		Expect(err).To(Equal(status.Error(codes.NotFound, "source snapshot content snapshot-1 not found")))
	})

	It("should create a volume with a volume datasource", func() {
		client := &ControllerClientMock{}
		client.datavolumes = make(map[string]*cdiv1.DataVolume)
		client.datavolumes[getKey(testInfraNamespace, "pvc-1")] = &cdiv1.DataVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pvc-1",
				Namespace: testInfraNamespace,
			},
		}
		controller := ControllerService{
			virtClient:              client,
			infraClusterNamespace:   testInfraNamespace,
			infraClusterLabels:      testInfraLabels,
			storageClassEnforcement: storageClassEnforcement,
		}

		request := getCreateVolumeRequest(getVolumeCapability(corev1.PersistentVolumeFilesystem, csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER))
		request.VolumeContentSource = &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Volume{
				Volume: &csi.VolumeContentSource_VolumeSource{
					VolumeId: "pvc-1",
				},
			},
		}

		_, err := controller.CreateVolume(context.TODO(), request)
		Expect(err).ToNot(HaveOccurred())
	})

	It("should fail to create a volume with a volume datasource, if volume not found", func() {
		client := &ControllerClientMock{}
		controller := ControllerService{
			virtClient:              client,
			infraClusterNamespace:   testInfraNamespace,
			infraClusterLabels:      testInfraLabels,
			storageClassEnforcement: storageClassEnforcement,
		}

		request := getCreateVolumeRequest(getVolumeCapability(corev1.PersistentVolumeFilesystem, csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER))
		request.VolumeContentSource = &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Volume{
				Volume: &csi.VolumeContentSource_VolumeSource{
					VolumeId: "pvc-1",
				},
			},
		}

		_, err := controller.CreateVolume(context.TODO(), request)
		Expect(err).To(HaveOccurred())
		Expect(err).To(Equal(status.Error(codes.NotFound, "source volume content pvc-1 not found")))
	})
})

var _ = Describe("DeleteVolume", func() {
	It("should successfully delete a volume", func() {
		client := &ControllerClientMock{}
		controller := ControllerService{
			virtClient:              client,
			infraClusterNamespace:   testInfraNamespace,
			infraClusterLabels:      testInfraLabels,
			storageClassEnforcement: storageClassEnforcement,
		}

		_, err := controller.DeleteVolume(context.TODO(), getDeleteVolumeRequest())
		Expect(err).ToNot(HaveOccurred())
	})

	It("should fail to delete a volume", func() {
		client := &ControllerClientMock{FailDeleteDataVolume: true}
		controller := ControllerService{
			virtClient:              client,
			infraClusterNamespace:   testInfraNamespace,
			infraClusterLabels:      testInfraLabels,
			storageClassEnforcement: storageClassEnforcement,
		}

		_, err := controller.DeleteVolume(context.TODO(), getDeleteVolumeRequest())
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("PublishUnPublish", func() {
	var (
		client     *ControllerClientMock
		controller *ControllerService
	)
	BeforeEach(func() {
		client = &ControllerClientMock{}
		controller = &ControllerService{
			virtClient:              client,
			infraClusterNamespace:   testInfraNamespace,
			infraClusterLabels:      testInfraLabels,
			storageClassEnforcement: storageClassEnforcement,
		}
	})

	It("should successfully publish", func() {
		dv, err := client.CreateDataVolume(context.TODO(), controller.infraClusterNamespace, &cdiv1.DataVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:   testVolumeName,
				Labels: testInfraLabels,
			},
			Spec: cdiv1.DataVolumeSpec{
				Storage: &cdiv1.StorageSpec{
					StorageClassName: &testInfraStorageClassName,
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("3Gi"),
						},
					},
				},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		client.datavolumes = make(map[string]*cdiv1.DataVolume)
		client.datavolumes[getKey(testInfraNamespace, testVolumeName)] = dv
		_, err = controller.ControllerPublishVolume(context.TODO(), getPublishVolumeRequest()) // AddVolumeToVM tests the hotplug request
		Expect(err).ToNot(HaveOccurred())
	})

	It("should successfully unpublish", func() {
		client.vmVolumes = []kubevirtv1.Volume{
			{
				Name: testVolumeName,
				VolumeSource: kubevirtv1.VolumeSource{
					DataVolume: &kubevirtv1.DataVolumeSource{
						Name:         testVolumeName,
						Hotpluggable: true,
					},
				},
			},
		}

		_, err := controller.ControllerUnpublishVolume(context.TODO(), getUnpublishVolumeRequest())
		Expect(err).ToNot(HaveOccurred())
	})

	It("should successfully unpublish when not hotplugged", func() {
		_, err := controller.ControllerUnpublishVolume(context.TODO(), getUnpublishVolumeRequest())
		Expect(err).ToNot(HaveOccurred())
	})

	It("should return success when unpublishing a volume from a VM that doesn't exist", func() {
		client.ShouldReturnVMNotFound = true
		req := getUnpublishVolumeRequest()
		req.NodeId = getKey(testInfraNamespace, "non-existent-node")
		_, err := controller.ControllerUnpublishVolume(context.TODO(), req)
		Expect(err).ToNot(HaveOccurred())
	})

	It("should unplug from VMI for carry over from old versions", func() {
		capturingClient := &vmiUnplugCapturingClient{
			ControllerClientMock: client,
		}
		controller.virtClient = capturingClient
		// The driver used to only hotplug to VMI in older versions
		capturingClient.virtualMachineStatus.VolumeStatus = append(client.virtualMachineStatus.VolumeStatus, kubevirtv1.VolumeStatus{
			Name:          testVolumeName,
			HotplugVolume: &kubevirtv1.HotplugVolumeStatus{},
		})
		capturingClient.vmVolumes = make([]kubevirtv1.Volume, 0)

		_, err := controller.ControllerUnpublishVolume(context.TODO(), getUnpublishVolumeRequest())
		Expect(err).ToNot(HaveOccurred())
		Expect(capturingClient.hotunplugForVMIOccured).To(BeTrue())
	})

	It("should not publish an RWO volume that is not yet released by another VMI", func() {
		// Create the DataVolume we will use.
		dv, err := client.CreateDataVolume(context.TODO(), controller.infraClusterNamespace, &cdiv1.DataVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:   testVolumeName,
				Labels: testInfraLabels,
			},
			Spec: cdiv1.DataVolumeSpec{
				Storage: &cdiv1.StorageSpec{
					StorageClassName: &testInfraStorageClassName,
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("3Gi"),
						},
					},
				},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		// Attach the volume to VM 1.
		client.datavolumes = make(map[string]*cdiv1.DataVolume)
		client.datavolumes[getKey(testInfraNamespace, testVolumeName)] = dv
		_, err = controller.ControllerPublishVolume(context.TODO(), getPublishVolumeRequest())
		Expect(err).ToNot(HaveOccurred())

		// Attempt to attach the volume to VM 2.
		client.ListVirtualMachineWithStatus = true
		_, err = controller.ControllerPublishVolume(context.TODO(), genPublishVolumeRequest(
			testVolumeName,
			getKey(testInfraNamespace, testVMName2),
			&csi.VolumeCapability{
				AccessMode: &csi.VolumeCapability_AccessMode{
					Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
				},
			},
		))
		Expect(err).To(HaveOccurred())
	})

	It("should publish an RWX volume that is not yet released by another VMI", func() {
		// Create the DataVolume we will use.
		dv, err := client.CreateDataVolume(context.TODO(), controller.infraClusterNamespace, &cdiv1.DataVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:   testVolumeName,
				Labels: testInfraLabels,
			},
			Spec: cdiv1.DataVolumeSpec{
				Storage: &cdiv1.StorageSpec{
					StorageClassName: &testInfraStorageClassName,
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("3Gi"),
						},
					},
				},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		// Attach the volume to VM 1.
		client.datavolumes = make(map[string]*cdiv1.DataVolume)
		client.datavolumes[getKey(testInfraNamespace, testVolumeName)] = dv
		_, err = controller.ControllerPublishVolume(context.TODO(), getPublishVolumeRequest())
		Expect(err).ToNot(HaveOccurred())

		// Attempt to attach the volume to VM 2.
		client.ListVirtualMachineWithStatus = true
		_, err = controller.ControllerPublishVolume(context.TODO(), genPublishVolumeRequest(
			testVolumeName,
			getKey(testInfraNamespace, testVMName2),
			&csi.VolumeCapability{
				AccessMode: &csi.VolumeCapability_AccessMode{
					Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
				},
			},
		))
		Expect(err).ToNot(HaveOccurred())
	})
})

var _ = Describe("Snapshots", func() {
	var (
		client     *ControllerClientMock
		controller *ControllerService
	)
	BeforeEach(func() {
		client = &ControllerClientMock{}
		controller = &ControllerService{
			virtClient:              client,
			infraClusterNamespace:   testInfraNamespace,
			infraClusterLabels:      testInfraLabels,
			storageClassEnforcement: storageClassEnforcement,
		}

	})

	Context("Create snapshots", func() {
		DescribeTable("should validate snapshot request", func(request *csi.CreateSnapshotRequest, expectedError error) {
			_, err := controller.CreateSnapshot(context.TODO(), request)
			Expect(err).To(Equal(expectedError))
		},
			Entry("should fail when request is missing", nil,
				status.Error(codes.InvalidArgument, "missing request")),
			Entry("should fail when name in request is missing", &csi.CreateSnapshotRequest{},
				status.Error(codes.InvalidArgument, "name missing in request")),
			Entry("should fail when the source volume ID in request is missing", &csi.CreateSnapshotRequest{
				Name: "snapshot-1",
			}, status.Error(codes.InvalidArgument, "source volume id missing in request")),
		)

		It("Should return an error if looking up existing snapshot errors", func() {
			client.FailGetSnapshot = true
			_, err := controller.CreateSnapshot(context.TODO(), &csi.CreateSnapshotRequest{
				Name:           "snapshot-1",
				SourceVolumeId: "pvc-123",
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("GetVolumeSnapshot failed"))
		})

		It("Should create a new snapshot if none found", func() {
			client.datavolumes = make(map[string]*cdiv1.DataVolume)
			client.datavolumes[getKey(testInfraNamespace, "pvc-123")] = &cdiv1.DataVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pvc-123",
					Namespace: testInfraNamespace,
				},
			}
			_, err := controller.CreateSnapshot(context.TODO(), &csi.CreateSnapshotRequest{
				Name:           "snapshot-2",
				SourceVolumeId: "pvc-123",
			})
			Expect(err).ToNot(HaveOccurred())
			newSnapshot := client.snapshots[getKey(testInfraNamespace, "snapshot-2")]
			Expect(newSnapshot).ToNot(BeNil())
		})

		It("Should return an error if looking up the datavolume fails", func() {
			client.FailGetDataVolume = true
			_, err := controller.CreateSnapshot(context.TODO(), &csi.CreateSnapshotRequest{
				Name:           "snapshot-1",
				SourceVolumeId: "pvc-123",
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("GetDataVolume failed"))
		})

		It("Should return not found if the source volume does not exist", func() {
			client.datavolumes = make(map[string]*cdiv1.DataVolume)
			_, err := controller.CreateSnapshot(context.TODO(), &csi.CreateSnapshotRequest{
				Name:           "snapshot-1",
				SourceVolumeId: "pvc-123",
			})
			Expect(err).To(HaveOccurred())
			Expect(err).To(Equal(status.Error(codes.NotFound, "source volume pvc-123 not found")))
		})

		It("Should create a new snapshot if none found", func() {
			client.FailCreateSnapshot = true
			client.datavolumes = make(map[string]*cdiv1.DataVolume)
			client.datavolumes[getKey(testInfraNamespace, "pvc-123")] = &cdiv1.DataVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pvc-123",
					Namespace: testInfraNamespace,
				},
			}
			_, err := controller.CreateSnapshot(context.TODO(), &csi.CreateSnapshotRequest{
				Name:           "snapshot-1",
				SourceVolumeId: "pvc-123",
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("CreateVolumeSnapshot failed"))
		})

		DescribeTable("Should reject creating snapshot if existing snapshot does not have a source volume", func(volumeName *string) {
			client.snapshots = make(map[string]*snapshotv1.VolumeSnapshot)
			client.snapshots[getKey(testInfraNamespace, "snapshot-1")] = &snapshotv1.VolumeSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "snapshot-1",
					Namespace: testInfraNamespace,
				},
				Spec: snapshotv1.VolumeSnapshotSpec{
					Source: snapshotv1.VolumeSnapshotSource{
						PersistentVolumeClaimName: volumeName,
					},
				},
			}
			_, err := controller.CreateSnapshot(context.TODO(), &csi.CreateSnapshotRequest{
				Name:           "snapshot-1",
				SourceVolumeId: "pvc-123",
			})
			Expect(err).To(HaveOccurred())
			Expect(err).To(Equal(status.Error(codes.AlreadyExists, "snapshot with the same name: snapshot-1 but with different SourceVolumeId already exist")))
		},
			Entry("Should reject creating snapshot if existing snapshot has nil source volume", nil),
			Entry("Should reject creating snapshot if existing snapshot has a different source volume", &testVolumeName),
		)

		It("Should return existing snapshot if the name and source volume match", func() {
			client.snapshots = make(map[string]*snapshotv1.VolumeSnapshot)
			client.snapshots[getKey(testInfraNamespace, "snapshot-1")] = &snapshotv1.VolumeSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "snapshot-1",
					Namespace: testInfraNamespace,
				},
				Spec: snapshotv1.VolumeSnapshotSpec{
					Source: snapshotv1.VolumeSnapshotSource{
						PersistentVolumeClaimName: ptr.To[string]("pvc-123"),
					},
				},
				Status: &snapshotv1.VolumeSnapshotStatus{
					ReadyToUse:  ptr.To[bool](true),
					RestoreSize: ptr.To[resource.Quantity](resource.MustParse("1Gi")),
				},
			}
			snapshot, err := controller.CreateSnapshot(context.TODO(), &csi.CreateSnapshotRequest{
				Name:           "snapshot-1",
				SourceVolumeId: "pvc-123",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(snapshot.Snapshot.SnapshotId).To(Equal("snapshot-1"))
			Expect(snapshot.Snapshot.ReadyToUse).To(BeTrue())
			Expect(snapshot.Snapshot.SizeBytes).To(Equal(int64(1073741824)))
		})
	})

	Context("Delete snapshots", func() {
		It("should reject deletion request if it is nil", func() {
			_, err := controller.DeleteSnapshot(context.TODO(), nil)
			Expect(err).To(HaveOccurred())
		})

		It("should reject deletion request if snapshot id is missing", func() {
			_, err := controller.DeleteSnapshot(context.TODO(), &csi.DeleteSnapshotRequest{
				SnapshotId: "",
			})
			Expect(err).To(HaveOccurred())
		})

		It("should return an error if the delete snapshot fails", func() {
			client.FailDeleteSnapshot = true
			_, err := controller.DeleteSnapshot(context.TODO(), &csi.DeleteSnapshotRequest{
				SnapshotId: "pvc-123",
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("DeleteVolumeSnapshot failed"))
		})

		It("should return success if delete snapshot succeeds", func() {
			_, err := controller.DeleteSnapshot(context.TODO(), &csi.DeleteSnapshotRequest{
				SnapshotId: "pvc-123",
			})
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("List snapshots", func() {
		createSnapshots := func(count int) {
			if client.snapshots == nil {
				client.snapshots = make(map[string]*snapshotv1.VolumeSnapshot)
			}
			for i := 0; i < count; i++ {
				client.snapshots[getKey(testInfraNamespace, fmt.Sprintf("snapshot-%d", i))] = &snapshotv1.VolumeSnapshot{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("snapshot-%d", i),
						Namespace: testInfraNamespace,
					},
					Spec: snapshotv1.VolumeSnapshotSpec{
						Source: snapshotv1.VolumeSnapshotSource{
							PersistentVolumeClaimName: ptr.To[string](fmt.Sprintf("pvc-%d", i)),
						},
					},
					Status: &snapshotv1.VolumeSnapshotStatus{
						ReadyToUse:  ptr.To[bool](true),
						RestoreSize: ptr.To[resource.Quantity](resource.MustParse("1Gi")),
					},
				}
			}
		}

		BeforeEach(func() {
			createSnapshots(10)
		})

		It("should reject a nil request", func() {
			_, err := controller.ListSnapshots(context.TODO(), nil)
			Expect(err).To(HaveOccurred())
		})

		It("should return all snapshots, if no count and max is supplied", func() {
			res, err := controller.ListSnapshots(context.TODO(), &csi.ListSnapshotsRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Entries).To(HaveLen(10))
			for _, entry := range res.GetEntries() {
				snapId := entry.Snapshot.SnapshotId
				_, ok := client.snapshots[getKey(testInfraNamespace, snapId)]
				Expect(ok).To(BeTrue())
			}
			Expect(res.GetNextToken()).To(BeEmpty())
		})

		It("should return error if list snapshots fails", func() {
			client.FailListSnapshots = true
			_, err := controller.ListSnapshots(context.TODO(), &csi.ListSnapshotsRequest{})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("ListVolumeSnapshots failed"))
		})

		It("should return a single snapshot if name is specified", func() {
			res, err := controller.ListSnapshots(context.TODO(), &csi.ListSnapshotsRequest{
				SnapshotId: "snapshot-5",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Entries).To(HaveLen(1))
			Expect(res.GetNextToken()).To(BeEmpty())
			Expect(res.Entries[0].Snapshot.SnapshotId).To(Equal("snapshot-5"))
		})

		It("should return an error if get snapshot fails", func() {
			client.FailGetSnapshot = true
			res, err := controller.ListSnapshots(context.TODO(), &csi.ListSnapshotsRequest{
				SnapshotId: "snapshot-5",
			})
			Expect(err).To(HaveOccurred())
			Expect(res).To(BeNil())
		})

		It("should return an empty result if snapshot not found", func() {
			res, err := controller.ListSnapshots(context.TODO(), &csi.ListSnapshotsRequest{
				SnapshotId: "snapshot-x",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Entries).To(BeNil())
		})

		It("should return all snapshots that match the source volume", func() {
			client.snapshots[getKey(testInfraNamespace, "snapshot-4")].Spec.Source.PersistentVolumeClaimName = ptr.To[string]("pvc-5")
			client.snapshots[getKey(testInfraNamespace, "snapshot-8")].Spec.Source.PersistentVolumeClaimName = ptr.To[string]("pvc-5")
			res, err := controller.ListSnapshots(context.TODO(), &csi.ListSnapshotsRequest{
				SourceVolumeId: "pvc-5",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Entries).To(HaveLen(3))
			Expect(res.GetNextToken()).To(BeEmpty())
			for _, entry := range res.GetEntries() {
				Expect(entry.Snapshot.SnapshotId).To(BeElementOf("snapshot-5", "snapshot-4", "snapshot-8"))
			}
		})

		It("should return an error if starting token is invalid (not int)", func() {
			res, err := controller.ListSnapshots(context.TODO(), &csi.ListSnapshotsRequest{
				StartingToken: "invalid",
			})
			Expect(err).To(HaveOccurred())
			Expect(res).To(BeNil())
		})

		It("should return all snapshots is request is larger than total", func() {
			res, err := controller.ListSnapshots(context.TODO(), &csi.ListSnapshotsRequest{
				MaxEntries: 100,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Entries).To(HaveLen(10))
			Expect(res.GetNextToken()).To(BeEmpty())
		})

		It("should return a subset if start token and max is set", func() {
			res, err := controller.ListSnapshots(context.TODO(), &csi.ListSnapshotsRequest{
				MaxEntries:    5,
				StartingToken: "2",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Entries).To(HaveLen(5))
			Expect(res.GetNextToken()).To(Equal("7"))
		})
	})
})

var _ = Describe("Expand", func() {
	var (
		client     *ControllerClientMock
		controller *ControllerService
	)
	BeforeEach(func() {
		client = &ControllerClientMock{}
		controller = &ControllerService{
			virtClient:              client,
			infraClusterNamespace:   testInfraNamespace,
			infraClusterLabels:      testInfraLabels,
			storageClassEnforcement: storageClassEnforcement,
		}
	})

	It("should successfully expand", func() {
		size := int64(1 * 1024 * 1024 * 1024)
		res, err := controller.ControllerExpandVolume(context.TODO(), &csi.ControllerExpandVolumeRequest{
			VolumeId: testVolumeName,
			VolumeCapability: &csi.VolumeCapability{
				AccessType: &csi.VolumeCapability_Mount{
					Mount: &csi.VolumeCapability_MountVolume{
						FsType: "ext4",
					},
				},
			},
			CapacityRange: &csi.CapacityRange{
				RequiredBytes: size,
			},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(res).To(HaveValue(Equal(csi.ControllerExpandVolumeResponse{
			CapacityBytes:         size,
			NodeExpansionRequired: true,
		})))
		Expect(client.ExpansionOccured).To(BeTrue())
		Expect(client.ExpansionVerified).To(BeTrue())
	})
})

//
// The rest of the file is code used by the tests and tests infrastructure
//

var (
	testVolumeName                                = "pvc-3d8be521-6e4b-4a87-add4-1961bf62f4ea"
	testInfraStorageClassName                     = "infra-storage"
	testVolumeStorageSize     int64               = 1024 * 1024 * 1024 * 3
	testInfraNamespace                            = "tenant-cluster-2"
	testNodeID                                    = getKey(testInfraNamespace, testVMName)
	testVMName                                    = "test-vm"
	testVMName2                                   = "test-vm2"
	testDataVolumeUID                             = "2d0111d5-494f-4731-8f67-122b27d3c366"
	testBusType               *kubevirtv1.DiskBus = nil // nil==do not pass bus type
	testInfraLabels                               = map[string]string{"infra-label-name": "infra-label-value"}
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

func getVolumeCapability(volumeMode corev1.PersistentVolumeMode, accessModes csi.VolumeCapability_AccessMode_Mode) *csi.VolumeCapability {
	var volumeCapability *csi.VolumeCapability

	if volumeMode == corev1.PersistentVolumeFilesystem {
		volumeCapability = &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{},
		}
	} else {
		volumeCapability = &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Block{
				Block: &csi.VolumeCapability_BlockVolume{},
			},
		}
	}

	volumeCapability.AccessMode = &csi.VolumeCapability_AccessMode{
		Mode: accessModes,
	}

	return volumeCapability
}

func getCreateVolumeRequest(volumeCapability *csi.VolumeCapability) *csi.CreateVolumeRequest {
	parameters := map[string]string{}
	if testInfraStorageClassName != "" {
		parameters[client.InfraStorageClassNameParameter] = testInfraStorageClassName
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

func genPublishVolumeRequest(volumeName, nodeID string, capabilty *csi.VolumeCapability) *csi.ControllerPublishVolumeRequest {
	return &csi.ControllerPublishVolumeRequest{
		VolumeId: volumeName,
		NodeId:   nodeID,
		VolumeContext: map[string]string{
			busParameter:    string(getBusType()),
			serialParameter: testDataVolumeUID,
		},
		VolumeCapability: capabilty,
	}
}

func getPublishVolumeRequest() *csi.ControllerPublishVolumeRequest {
	return genPublishVolumeRequest(testVolumeName, testNodeID, &csi.VolumeCapability{})
}

func getUnpublishVolumeRequest() *csi.ControllerUnpublishVolumeRequest {
	return &csi.ControllerUnpublishVolumeRequest{
		VolumeId: testVolumeName,
		NodeId:   testNodeID,
	}
}

type ControllerClientMock struct {
	FailListVirtualMachines      bool
	ListVirtualMachineWithStatus bool
	FailDeleteDataVolume         bool
	FailCreateDataVolume         bool
	FailGetDataVolume            bool
	FailAddVolumeToVM            bool
	FailRemoveVolumeFromVM       bool
	FailGetSnapshot              bool
	FailCreateSnapshot           bool
	FailDeleteSnapshot           bool
	FailListSnapshots            bool
	ShouldReturnVMNotFound       bool
	ExpansionOccured             bool
	ExpansionVerified            bool
	virtualMachineStatus         kubevirtv1.VirtualMachineInstanceStatus
	vmVolumes                    []kubevirtv1.Volume
	snapshots                    map[string]*snapshotv1.VolumeSnapshot
	datavolumes                  map[string]*cdiv1.DataVolume
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
func (c *ControllerClientMock) ListVirtualMachines(_ context.Context, namespace string) ([]kubevirtv1.VirtualMachineInstance, error) {
	if c.FailListVirtualMachines {
		return nil, errors.New("ListVirtualMachines failed")
	}

	if c.ListVirtualMachineWithStatus {
		return []kubevirtv1.VirtualMachineInstance{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testVMName,
					Namespace: namespace,
				},
				Status: kubevirtv1.VirtualMachineInstanceStatus{
					VolumeStatus: []kubevirtv1.VolumeStatus{
						{
							Name: testVolumeName,
						},
					},
				},
			},
		}, nil
	}

	return []kubevirtv1.VirtualMachineInstance{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testVMName,
				Namespace: namespace,
			},
		},
	}, nil
}

func (c *ControllerClientMock) GetVirtualMachine(_ context.Context, namespace, name string) (*kubevirtv1.VirtualMachineInstance, error) {
	if c.FailListVirtualMachines {
		return nil, errors.New("ListVirtualMachines failed")
	}

	return &kubevirtv1.VirtualMachineInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec:   kubevirtv1.VirtualMachineInstanceSpec{},
		Status: c.virtualMachineStatus,
	}, nil
}

func (c *ControllerClientMock) GetWorkloadManagingVirtualMachine(_ context.Context, namespace, name string) (*kubevirtv1.VirtualMachine, error) {
	if c.ShouldReturnVMNotFound {
		return nil, k8serrors.NewNotFound(corev1.Resource("vm"), name)
	}
	volumes := make([]kubevirtv1.Volume, 0)
	volumes = append(volumes, c.vmVolumes...)

	return &kubevirtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: kubevirtv1.VirtualMachineSpec{
			Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
				Spec: kubevirtv1.VirtualMachineInstanceSpec{
					Volumes: volumes,
				},
			},
		},
	}, nil
}

func (c *ControllerClientMock) DeleteDataVolume(_ context.Context, namespace string, name string) error {
	if c.FailDeleteDataVolume {
		return errors.New("DeleteDataVolume failed")
	}
	// Test input
	Expect(testVolumeName).To(Equal(name))

	return nil
}
func (c *ControllerClientMock) CreateDataVolume(_ context.Context, namespace string, dataVolume *cdiv1.DataVolume) (*cdiv1.DataVolume, error) {
	if c.FailCreateDataVolume {
		return nil, errors.New("CreateDataVolume failed")
	}

	// Test input
	Expect(testVolumeName).To(Equal(dataVolume.GetName()))
	if testInfraStorageClassName != "" {
		Expect(testInfraStorageClassName).To(Equal(*dataVolume.Spec.Storage.StorageClassName))
	} else {
		Expect(dataVolume.Spec.Storage.StorageClassName).To(BeNil())
	}
	q, ok := dataVolume.Spec.Storage.Resources.Requests[corev1.ResourceStorage]
	Expect(ok).To(BeTrue())
	Expect(testVolumeStorageSize).To(Equal(q.Value()))
	Expect(testInfraLabels).To(Equal(dataVolume.Labels))

	// Input OK. Now prepare result
	result := dataVolume.DeepCopy()

	result.SetUID(types.UID(testDataVolumeUID))

	if c.datavolumes == nil {
		c.datavolumes = make(map[string]*cdiv1.DataVolume)
	}
	c.datavolumes[getKey(namespace, dataVolume.Name)] = dataVolume

	return result, nil
}
func (c *ControllerClientMock) GetDataVolume(_ context.Context, namespace string, name string) (*cdiv1.DataVolume, error) {
	if c.FailGetDataVolume {
		return nil, errors.New("GetDataVolume failed")
	}
	dv, ok := c.datavolumes[getKey(namespace, name)]
	if !ok {
		return nil, k8serrors.NewNotFound(cdiv1.Resource("DataVolume"), name)
	}
	return dv, nil
}
func (c *ControllerClientMock) GetPersistentVolumeClaim(_ context.Context, namespace string, claimName string) (*corev1.PersistentVolumeClaim, error) {
	return nil, errors.New("Not implemented")
}
func (c *ControllerClientMock) ExpandPersistentVolumeClaim(_ context.Context, namespace string, claimName string, size int64) error {
	c.ExpansionOccured = true
	return nil
}
func (c *ControllerClientMock) ListDataVolumes(_ context.Context, namespace string) ([]cdiv1.DataVolume, error) {
	return nil, errors.New("Not implemented")
}
func (c *ControllerClientMock) GetVMI(ctx context.Context, namespace string, name string) (*kubevirtv1.VirtualMachineInstance, error) {
	return nil, errors.New("Not implemented")
}
func (c *ControllerClientMock) AddVolumeToVM(_ context.Context, namespace string, vmName string, addVolumeOptions *kubevirtv1.AddVolumeOptions) error {
	if c.FailAddVolumeToVM {
		return errors.New("AddVolumeToVM failed")
	}

	// Test input
	Expect(testVolumeName).To(Equal(addVolumeOptions.Name))
	Expect(testVolumeName).To(Equal(addVolumeOptions.VolumeSource.DataVolume.Name))
	Expect(getBusType()).To(Equal(addVolumeOptions.Disk.DiskDevice.Disk.Bus))
	Expect(testDataVolumeUID).To(Equal(addVolumeOptions.Disk.Serial))

	return nil
}
func (c *ControllerClientMock) RemoveVolumeFromVM(_ context.Context, namespace string, vmName string, removeVolumeOptions *kubevirtv1.RemoveVolumeOptions) error {
	if c.FailRemoveVolumeFromVM {
		return errors.New("RemoveVolumeFromVM failed")
	}

	// Test input
	Expect(testVMName).To(Equal(vmName))
	Expect(testVolumeName).To(Equal(removeVolumeOptions.Name))

	return nil
}
func (c *ControllerClientMock) RemoveVolumeFromVMI(_ context.Context, namespace string, vmName string, removeVolumeOptions *kubevirtv1.RemoveVolumeOptions) error {
	return nil
}

func (c *ControllerClientMock) EnsureVolumeAvailable(_ context.Context, namespace, vmName, volumeName string, timeout time.Duration) error {
	return nil
}

func (c *ControllerClientMock) EnsureVolumeRemoved(_ context.Context, namespace, vmName, volumeName string, timeout time.Duration) error {
	return nil
}

func (c *ControllerClientMock) EnsureSnapshotReady(_ context.Context, namespace, name string, timeout time.Duration) error {
	return nil
}

func (c *ControllerClientMock) EnsureControllerResize(_ context.Context, namespace, claimName string, timeout time.Duration) error {
	c.ExpansionVerified = true
	return nil
}

func (c *ControllerClientMock) EnsureVolumeAvailableVM(_ context.Context, namespace, vmName, volName string) (bool, error) {
	return false, nil
}

func (c *ControllerClientMock) EnsureVolumeRemovedVM(_ context.Context, namespace, vmName, volName string) (bool, error) {
	return false, nil
}

func (c *ControllerClientMock) CreateVolumeSnapshot(ctx context.Context, namespace, name, claimName, snapshotClassName string) (*snapshotv1.VolumeSnapshot, error) {
	if c.FailCreateSnapshot {
		return nil, errors.New("CreateVolumeSnapshot failed")
	}
	if c.snapshots == nil {
		c.snapshots = make(map[string]*snapshotv1.VolumeSnapshot)
	}
	c.snapshots[getKey(namespace, name)] = &snapshotv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: snapshotv1.VolumeSnapshotSpec{
			Source: snapshotv1.VolumeSnapshotSource{
				PersistentVolumeClaimName: ptr.To[string](claimName),
			},
		},
		Status: &snapshotv1.VolumeSnapshotStatus{
			ReadyToUse:  ptr.To[bool](true),
			RestoreSize: ptr.To[resource.Quantity](resource.MustParse("1Gi")),
		},
	}
	return c.snapshots[getKey(namespace, name)], nil
}

func (c *ControllerClientMock) GetVolumeSnapshot(ctx context.Context, namespace, name string) (*snapshotv1.VolumeSnapshot, error) {
	if c.FailGetSnapshot {
		return nil, errors.New("GetVolumeSnapshot failed")
	}
	if c.snapshots == nil {
		c.snapshots = make(map[string]*snapshotv1.VolumeSnapshot)
	}
	snapshot, ok := c.snapshots[getKey(namespace, name)]
	if !ok {
		return nil, k8serrors.NewNotFound(snapshotv1.Resource("VolumeSnapshot"), name)
	}
	return snapshot, nil
}

func (c *ControllerClientMock) DeleteVolumeSnapshot(ctx context.Context, namespace, name string) error {
	if c.FailDeleteSnapshot {
		return errors.New("DeleteVolumeSnapshot failed")
	}
	if c.snapshots == nil {
		c.snapshots = make(map[string]*snapshotv1.VolumeSnapshot)
	}
	delete(c.snapshots, getKey(namespace, name))
	return nil
}

func (c *ControllerClientMock) ListVolumeSnapshots(ctx context.Context, namespace string) (*snapshotv1.VolumeSnapshotList, error) {
	if c.FailListSnapshots {
		return nil, errors.New("ListVolumeSnapshots failed")
	}
	if c.snapshots == nil {
		c.snapshots = make(map[string]*snapshotv1.VolumeSnapshot)
	}
	res := &snapshotv1.VolumeSnapshotList{}
	for _, v := range c.snapshots {
		res.Items = append(res.Items, *v)
	}
	return res, nil
}

type vmiUnplugCapturingClient struct {
	*ControllerClientMock
	hotunplugForVMIOccured bool
}

func (c *vmiUnplugCapturingClient) RemoveVolumeFromVMI(_ context.Context, namespace string, vmName string, removeVolumeOptions *kubevirtv1.RemoveVolumeOptions) error {
	c.hotunplugForVMIOccured = true

	return nil
}

func getKey(namespace, name string) string {
	return fmt.Sprintf("%s/%s", namespace, name)
}

var _ = Describe("Fast-path publish / unpublish", func() {
	It("should skip hot-plug when the disk is already attached", func() {
		cli := &attachSkipClient{ControllerClientMock: &ControllerClientMock{}}

		// fake DataVolume so GetDataVolume succeeds
		cli.datavolumes = map[string]*cdiv1.DataVolume{
			getKey(testInfraNamespace, testVolumeName): {
				ObjectMeta: metav1.ObjectMeta{
					Name:      testVolumeName,
					Namespace: testInfraNamespace,
				},
			},
		}

		ctrl := &ControllerService{
			virtClient:              cli,
			infraClusterNamespace:   testInfraNamespace,
			infraClusterLabels:      testInfraLabels,
			storageClassEnforcement: storageClassEnforcement,
		}

		_, err := ctrl.ControllerPublishVolume(
			context.TODO(), getPublishVolumeRequest())
		Expect(err).ToNot(HaveOccurred())

		Expect(cli.addCnt).To(Equal(0))    // hot-plug was skipped
		Expect(cli.ensureCnt).To(Equal(1)) // fast-path check executed
	})

	It("should skip hot-unplug when the disk is already detached", func() {
		cli := &detachSkipClient{ControllerClientMock: &ControllerClientMock{}}

		ctrl := &ControllerService{
			virtClient:              cli,
			infraClusterNamespace:   testInfraNamespace,
			infraClusterLabels:      testInfraLabels,
			storageClassEnforcement: storageClassEnforcement,
		}

		_, err := ctrl.ControllerUnpublishVolume(
			context.TODO(), getUnpublishVolumeRequest())
		Expect(err).ToNot(HaveOccurred())

		Expect(cli.removeCnt).To(Equal(0)) // unplug skipped
		Expect(cli.ensureCnt).To(Equal(1)) // fast-path check executed
	})
})

// returns "already attached", records calls
type attachSkipClient struct {
	*ControllerClientMock
	addCnt    int // AddVolumeToVM called
	ensureCnt int // EnsureVolumeAvailable(VM) called
}

// the driver never calls AddVolumeToVM when it detects an
// already-attached disk, but we still implement it for safety.
func (c *attachSkipClient) AddVolumeToVM(_ context.Context,
	ns, vm string, opts *kubevirtv1.AddVolumeOptions) error {

	c.addCnt++
	return nil
}

// EnsureVolumeAvailableVM is the test hook for fast-path detection.
// Return (true,nil) to say "already attached".
func (c *attachSkipClient) EnsureVolumeAvailableVM(_ context.Context,
	ns, vm, dv string) (bool, error) {

	c.ensureCnt++
	return true, nil
}

type detachSkipClient struct {
	*ControllerClientMock
	removeCnt int // RemoveVolumeFromVM called
	ensureCnt int // EnsureVolumeRemoved(VM) called
}

func (c *detachSkipClient) RemoveVolumeFromVM(_ context.Context,
	ns, vm string, opts *kubevirtv1.RemoveVolumeOptions) error {

	c.removeCnt++
	return nil
}

func (c *detachSkipClient) EnsureVolumeRemovedVM(_ context.Context,
	ns, vm, dv string) (bool, error) {

	c.ensureCnt++
	return true, nil // volume absent
}
