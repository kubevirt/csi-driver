package util

type StorageClassEnforcement struct {
	AllowList              []string                 `yaml:"allowList"`
	AllowAll               bool                     `yaml:"allowAll"`
	AllowDefault           bool                     `yaml:"allowDefault"`
	StorageSnapshotMapping []StorageSnapshotMapping `yaml:"storageSnapshotMapping,omitempty"`
	// InfraStorageClassLabels adds labels to a DataVolume created on that
	// infra storage class. Keys must not overlap the cluster-wide infra
	// labels, which the driver uses as an ownership check.
	InfraStorageClassLabels map[string]map[string]string `yaml:"infraStorageClassLabels,omitempty"`
}

type StorageSnapshotMapping struct {
	VolumeSnapshotClasses []string `yaml:"volumeSnapshotClasses,omitempty"`
	StorageClasses        []string `yaml:"storageClasses"`
}

// Contains tells whether a contains x.
func Contains(arr []string, val string) bool {
	for _, itrVal := range arr {
		if val == itrVal {
			return true
		}
	}
	return false
}
