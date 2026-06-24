package service

import (
	"context"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubevirtv1 "kubevirt.io/api/core/v1"
)

// reconcilerMock is a hand-rolled minimal kubevirt.Client implementation
// scoped to the DetachReconciler behaviours. It embeds ControllerClientMock
// so we only override what the reconciler exercises.
type reconcilerMock struct {
	*ControllerClientMock

	mu              sync.Mutex
	vmis            []kubevirtv1.VirtualMachineInstance
	vmMissingByName map[string]bool
	vmSpecVolumes   map[string][]string // vm name → volume names in spec
	removed         []orphanKey         // recorded RemoveVolumeFromVMI calls
}

func newReconcilerMock() *reconcilerMock {
	return &reconcilerMock{
		ControllerClientMock: &ControllerClientMock{},
		vmMissingByName:      map[string]bool{},
		vmSpecVolumes:        map[string][]string{},
	}
}

func (m *reconcilerMock) ListVirtualMachines(_ context.Context, _ string) ([]kubevirtv1.VirtualMachineInstance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]kubevirtv1.VirtualMachineInstance, len(m.vmis))
	copy(out, m.vmis)
	return out, nil
}

func (m *reconcilerMock) GetWorkloadManagingVirtualMachine(_ context.Context, namespace, name string) (*kubevirtv1.VirtualMachine, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.vmMissingByName[name] {
		return nil, k8serrors.NewNotFound(corev1.Resource("virtualmachine"), name)
	}
	vols := []kubevirtv1.Volume{}
	for _, n := range m.vmSpecVolumes[name] {
		vols = append(vols, kubevirtv1.Volume{Name: n})
	}
	return &kubevirtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: kubevirtv1.VirtualMachineSpec{
			Template: &kubevirtv1.VirtualMachineInstanceTemplateSpec{
				Spec: kubevirtv1.VirtualMachineInstanceSpec{
					Volumes: vols,
				},
			},
		},
	}, nil
}

func (m *reconcilerMock) RemoveVolumeFromVMI(_ context.Context, _ string, vmName string, opts *kubevirtv1.RemoveVolumeOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removed = append(m.removed, orphanKey{vmiName: vmName, volumeName: opts.Name})
	return nil
}

func (m *reconcilerMock) takeRemoved() []orphanKey {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]orphanKey{}, m.removed...)
	m.removed = nil
	return out
}

func makeVMIWithHotplugs(name string, hotplugs ...string) kubevirtv1.VirtualMachineInstance {
	vmi := kubevirtv1.VirtualMachineInstance{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	for _, h := range hotplugs {
		vmi.Status.VolumeStatus = append(vmi.Status.VolumeStatus, kubevirtv1.VolumeStatus{
			Name:          h,
			HotplugVolume: &kubevirtv1.HotplugVolumeStatus{},
		})
	}
	return vmi
}

var _ = Describe("DetachReconciler", func() {
	const ns = "tenant-foo"

	It("requires two passes spaced by gracePeriod before acting on an orphan", func() {
		mock := newReconcilerMock()
		// VMI carries a hot-plug not present in VM spec → orphan.
		mock.vmis = []kubevirtv1.VirtualMachineInstance{
			makeVMIWithHotplugs("vm-a", "pvc-orphan"),
		}
		mock.vmSpecVolumes["vm-a"] = []string{} // empty: orphan

		// gracePeriod is generous; we drive time manually via two Sync calls
		// separated by a sleep slightly longer than the grace period.
		r := NewDetachReconciler(mock, ns, time.Hour, 10*time.Millisecond)

		Expect(r.Sync(context.TODO())).To(Succeed())
		Expect(mock.takeRemoved()).To(BeEmpty(), "should not act on first observation")

		time.Sleep(15 * time.Millisecond)

		Expect(r.Sync(context.TODO())).To(Succeed())
		Expect(mock.takeRemoved()).To(ConsistOf(orphanKey{vmiName: "vm-a", volumeName: "pvc-orphan"}))
	})

	It("does not touch hot-plugs that are still in VM spec", func() {
		mock := newReconcilerMock()
		mock.vmis = []kubevirtv1.VirtualMachineInstance{
			makeVMIWithHotplugs("vm-a", "pvc-legit"),
		}
		mock.vmSpecVolumes["vm-a"] = []string{"pvc-legit"}

		r := NewDetachReconciler(mock, ns, time.Hour, 1*time.Millisecond)

		Expect(r.Sync(context.TODO())).To(Succeed())
		time.Sleep(5 * time.Millisecond)
		Expect(r.Sync(context.TODO())).To(Succeed())

		Expect(mock.takeRemoved()).To(BeEmpty())
	})

	It("treats every hot-plug on a VMI as orphan when the parent VM is gone", func() {
		mock := newReconcilerMock()
		mock.vmis = []kubevirtv1.VirtualMachineInstance{
			makeVMIWithHotplugs("vm-a", "pvc-1", "pvc-2"),
		}
		mock.vmMissingByName["vm-a"] = true

		r := NewDetachReconciler(mock, ns, time.Hour, 1*time.Millisecond)

		Expect(r.Sync(context.TODO())).To(Succeed())
		time.Sleep(5 * time.Millisecond)
		Expect(r.Sync(context.TODO())).To(Succeed())

		Expect(mock.takeRemoved()).To(ConsistOf(
			orphanKey{vmiName: "vm-a", volumeName: "pvc-1"},
			orphanKey{vmiName: "vm-a", volumeName: "pvc-2"},
		))
	})

	It("skips terminating VMIs", func() {
		now := metav1.Now()
		mock := newReconcilerMock()
		vmi := makeVMIWithHotplugs("vm-a", "pvc-orphan")
		vmi.DeletionTimestamp = &now
		mock.vmis = []kubevirtv1.VirtualMachineInstance{vmi}
		mock.vmSpecVolumes["vm-a"] = []string{}

		r := NewDetachReconciler(mock, ns, time.Hour, 1*time.Millisecond)

		Expect(r.Sync(context.TODO())).To(Succeed())
		time.Sleep(5 * time.Millisecond)
		Expect(r.Sync(context.TODO())).To(Succeed())

		Expect(mock.takeRemoved()).To(BeEmpty())
	})

	It("prunes seen entries that are no longer divergent", func() {
		mock := newReconcilerMock()
		mock.vmis = []kubevirtv1.VirtualMachineInstance{
			makeVMIWithHotplugs("vm-a", "pvc-X"),
		}
		mock.vmSpecVolumes["vm-a"] = []string{}                  // orphan
		r := NewDetachReconciler(mock, ns, time.Hour, time.Hour) // gracePeriod long enough never to fire

		Expect(r.Sync(context.TODO())).To(Succeed())
		// internal seen map must have one entry
		Expect(r.seen).To(HaveLen(1))

		// Simulate VM coming back / volume re-added to spec.
		mock.vmSpecVolumes["vm-a"] = []string{"pvc-X"}
		Expect(r.Sync(context.TODO())).To(Succeed())
		Expect(r.seen).To(BeEmpty())
	})
})
