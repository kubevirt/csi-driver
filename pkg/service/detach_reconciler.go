package service

import (
	"context"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	klog "k8s.io/klog/v2"
	kubevirtv1 "kubevirt.io/api/core/v1"

	"kubevirt.io/csi-driver/pkg/kubevirt"
)

// Default cadence values for the orphan hot-plug reconciler. They are
// intentionally conservative: the reconciler is a backstop, not a hot path,
// and its work is proportional to the number of VMIs in the infra namespace.
const (
	defaultDetachSyncPeriod  = 5 * time.Minute
	defaultDetachGracePeriod = 5 * time.Minute
)

// DetachReconciler periodically scans VMIs in the infra cluster namespace
// and removes hot-plug volumes that are present in VMI.Status.VolumeStatus
// but no longer referenced by the parent VM's spec (or whose parent VM is
// gone altogether). Such "orphan" hot-plugs are leaked by
// ControllerUnpublishVolume in several edge cases — VM teardown races, virt
// spec/status divergence, external-attacher giving up after force-detach
// timeout — and they block subsequent attachments because the underlying
// infra storage device stays Primary/exclusively-attached on the original
// host. See the issue referenced in the PR that introduced this file for the
// full failure-mode catalogue.
//
// The reconciler is opt-in (controlled by --enable-detach-reconciler) and
// uses a two-pass "first-seen + grace period" filter to absorb transient
// divergence around live migrations and normal removevolume API calls. It
// never touches a hot-plug that is still referenced by the VM spec.
type DetachReconciler struct {
	virtClient            kubevirt.Client
	infraClusterNamespace string
	syncPeriod            time.Duration
	gracePeriod           time.Duration

	mu   sync.Mutex
	seen map[orphanKey]time.Time
}

// orphanKey identifies a (VMI, hot-plug volume name) pair.
type orphanKey struct {
	vmiName    string
	volumeName string
}

// NewDetachReconciler constructs a DetachReconciler. A zero syncPeriod or
// gracePeriod selects the package defaults.
func NewDetachReconciler(
	virtClient kubevirt.Client,
	infraClusterNamespace string,
	syncPeriod, gracePeriod time.Duration,
) *DetachReconciler {
	if syncPeriod <= 0 {
		syncPeriod = defaultDetachSyncPeriod
	}
	if gracePeriod <= 0 {
		gracePeriod = defaultDetachGracePeriod
	}
	return &DetachReconciler{
		virtClient:            virtClient,
		infraClusterNamespace: infraClusterNamespace,
		syncPeriod:            syncPeriod,
		gracePeriod:           gracePeriod,
		seen:                  make(map[orphanKey]time.Time),
	}
}

// Run blocks until ctx is cancelled, executing Sync once at startup and then
// every syncPeriod thereafter. Errors from Sync are logged but never abort
// the loop — a transient infra-cluster API failure must not stop reconciliation.
func (r *DetachReconciler) Run(ctx context.Context) error {
	klog.Infof("detach-reconciler: starting, namespace=%q syncPeriod=%s gracePeriod=%s",
		r.infraClusterNamespace, r.syncPeriod, r.gracePeriod)
	return wait.PollUntilContextCancel(ctx, r.syncPeriod, true, func(ctx context.Context) (bool, error) {
		if err := r.Sync(ctx); err != nil {
			klog.Warningf("detach-reconciler: sync error: %v", err)
		}
		return false, nil
	})
}

// Sync performs one reconciliation pass. Public for ease of testing.
func (r *DetachReconciler) Sync(ctx context.Context) error {
	vmis, err := r.virtClient.ListVirtualMachines(ctx, r.infraClusterNamespace)
	if err != nil {
		return err
	}

	// Build the set of orphans observed in this pass, so we can prune
	// `seen` entries that are no longer divergent.
	observed := make(map[orphanKey]struct{})
	now := time.Now()

	for i := range vmis {
		vmi := &vmis[i]
		if vmi.DeletionTimestamp != nil {
			// VMI is terminating; KubeVirt will tear down its hot-plug pods.
			continue
		}
		// Look up the parent VM. If it is gone, every hot-plug currently
		// reported by VMI.Status is an orphan.
		vm, vmErr := r.virtClient.GetWorkloadManagingVirtualMachine(ctx, r.infraClusterNamespace, vmi.Name)
		vmMissing := errors.IsNotFound(vmErr)
		if vmErr != nil && !vmMissing {
			klog.V(4).Infof("detach-reconciler: get VM %s/%s: %v",
				r.infraClusterNamespace, vmi.Name, vmErr)
			continue
		}

		for _, vs := range vmi.Status.VolumeStatus {
			if vs.HotplugVolume == nil {
				continue
			}
			if !vmMissing && volumeNameInVMSpec(vm, vs.Name) {
				continue // legit hot-plug
			}
			key := orphanKey{vmiName: vmi.Name, volumeName: vs.Name}
			observed[key] = struct{}{}
			if r.shouldAct(key, now) {
				klog.Infof("detach-reconciler: removing orphan hotplug %q from VMI %s/%s (vmMissing=%v)",
					vs.Name, r.infraClusterNamespace, vmi.Name, vmMissing)
				if err := r.virtClient.RemoveVolumeFromVMI(ctx, r.infraClusterNamespace, vmi.Name,
					&kubevirtv1.RemoveVolumeOptions{Name: vs.Name}); err != nil {
					klog.Warningf("detach-reconciler: RemoveVolumeFromVMI %s on %s/%s: %v",
						vs.Name, r.infraClusterNamespace, vmi.Name, err)
				}
				// On success we keep the seen entry so the next pass can
				// still find it if KubeVirt hasn't unplugged yet; it will
				// either disappear from VMI.Status (good) or stay (we retry).
			}
		}
	}

	r.pruneSeen(observed)
	return nil
}

// shouldAct returns true when this orphan has been observed for at least
// gracePeriod. It also records first-seen time for orphans seen for the
// first time.
func (r *DetachReconciler) shouldAct(k orphanKey, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	firstSeen, ok := r.seen[k]
	if !ok {
		r.seen[k] = now
		return false
	}
	return now.Sub(firstSeen) >= r.gracePeriod
}

// pruneSeen removes seen entries that are no longer observed as orphan, so
// the map does not grow unboundedly across the driver's lifetime.
func (r *DetachReconciler) pruneSeen(observed map[orphanKey]struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k := range r.seen {
		if _, still := observed[k]; !still {
			delete(r.seen, k)
		}
	}
}

func volumeNameInVMSpec(vm *kubevirtv1.VirtualMachine, name string) bool {
	if vm == nil || vm.Spec.Template == nil {
		return false
	}
	for _, v := range vm.Spec.Template.Spec.Volumes {
		if v.Name == name {
			return true
		}
	}
	return false
}
