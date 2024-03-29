# Configuring infra volume snapshot classes to map to tenant volume snapshot classes
It is possible to map multiple infra storage classes to multiple matching tenant storage classes. For instance if the infra cluster has 2 completely separate storage classes like storage class a and b, we can map them to tenant storage classes x and y. In order to define this mapping one should create a config map in the namespace of the infra storage classes that is used by the tenant storage class. This config map will have a few key value pairs that define the expected behavior.

* allowAll: If allow all is true, then allow all available storage classes in the infra cluster to be mapping to storage classes in the tenant cluster. If false, then use the allowList to limit which storage classes are visible to the tenant.
* allowDefault: If true, then no explicit mapping needs to be defined, and the driver will attempt to use the default storage class and default volume snapshot class of the infra cluster to satisfy requests from the tenant cluster
* allowList: A comma separated string list of all the allowed infra storage classes. Only used if allowAll is false.
* storageSnapshotMapping: Groups lists of infra storage classes and infra volume snapshot classes together. If in the same grouping then creating a snapshot using any of the listed volume snapshot class should work with any of the listed storage classes. Should only contain volume snapshot classes that are compatible with the listed storage classes. This is needed because it is not always possible to determine using the SA of the csi driver controller which volume snapshot classes go together with which storage classes.

## Example driver configs

### allowDefault
The simplest driver config takes the default storage class and default volume snapshot class and uses them, and no storage classes are restricted:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: driver-config
  namespace: example-namespace
data:
  infraClusterLabels: random-cluster-id #label used to distinguish between tenant clusters, if multiple clusters in same namespace
  infraClusterNamespace: example-namespace #Used to tell the tenant cluster which namespace it lives in
  infraStorageClassEnforcement: |
    allowAll: true
    allowDefault: true
```

### allowAll false, with default
The simplest driver config takes the default storage class and default volume snapshot class and uses them, and restricted to storage class a and b. Either storage class a or b should be the default:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: driver-config
  namespace: example-namespace
data:
  infraClusterLabels: random-cluster-id #label used to distinguish between tenant clusters, if multiple clusters in same namespace
  infraClusterNamespace: example-namespace #Used to tell the tenant cluster which namespace it lives in
  infraStorageClassEnforcement: |
    allowAll: false
    allowDefault: true
    allowList: [storage_class_a, storage_class_b]
```
Note ensure that the infra cluster has a default snapshot class defined, otherwise creation of the infra cluster snapshots will fail due to a missing snapshot class value.

### Specify which storage class maps to which volume snapshot class, unrelated storage classes
The infra cluster has multiple storage classes and they map to volume snapshot classes. The storage classes are not related and require different volume snapshot classes

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: driver-config
  namespace: example-namespace
data:
  infraClusterLabels: random-cluster-id #label used to distinguish between tenant clusters, if multiple clusters in same namespace
  infraClusterNamespace: example-namespace #Used to tell the tenant cluster which namespace it lives in
  infraStorageClassEnforcement: |
    allowAll: false
    allowDefault: true
    allowList: [storage_class_a, storage_class_b]
    storageSnapshotMapping: - StorageClasses:
      - storage_class_a
      VolumeSnapshotClasses:
      - volumesnapshot_class_a
    - StorageClasses:
      - storage_class_b
      VolumeSnapshotClasses:
      - volumesnapshot_class_b
```
If one tries to create a snapshot using volume snapshot class `kubevirt_csi_vsc_y` on a PVC associated with storage class `storage_class_x`. The CSI driver will reject that request and return an error containig a list of valid volume snapshot classes. In this case `kubevirt_csi_vsc_x`.

### Specify which storage class maps to which volume snapshot class, related storage classes
The infra cluster has multiple storage classes and they map to volume snapshot classes. The storage classes are not related and require different volume snapshot classes

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: driver-config
  namespace: example-namespace
data:
  infraClusterLabels: random-cluster-id #label used to distinguish between tenant clusters, if multiple clusters in same namespace
  infraClusterNamespace: example-namespace #Used to tell the tenant cluster which namespace it lives in
  infraStorageClassEnforcement: |
    allowAll: false
    allowDefault: true
    allowList: [storage_class_a, storage_class_b]
    storageSnapshotMapping: - StorageClasses:
      - storage_class_a
      - storage_class_b
      VolumeSnapshotClasses:
      - volumesnapshot_class_a
      - volumesnapshot_class_b
```
In this case, both storage classes and volumesnapshot classes are in the same `StorageClasses` group, so now trying to create a snapshot using `kubevirt_csi_vsc_y` of a PVC from storage class `storage_class_x` will succeed because that volume snapshot class is part of the group associated with that storage class.