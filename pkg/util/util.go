package util

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type StorageClassEnforcement struct {
	AllowList              []string                 `yaml:"allowList"`
	AllowAll               bool                     `yaml:"allowAll"`
	AllowDefault           bool                     `yaml:"allowDefault"`
	StorageSnapshotMapping []StorageSnapshotMapping `yaml:"storageSnapshotMapping,omitempty"`
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

// AddFinalizer accepts an Object and adds the provided finalizer if not present.
// It returns an indication of whether it updated the object's list of finalizers.
func AddFinalizer(o metav1.Object, finalizer string) (finalizersUpdated bool) {
	f := o.GetFinalizers()
	for _, e := range f {
		if e == finalizer {
			return false
		}
	}
	o.SetFinalizers(append(f, finalizer))
	return true
}

// RemoveFinalizer accepts an Object and removes the provided finalizer if present.
// It returns an indication of whether it updated the object's list of finalizers.
func RemoveFinalizer(o metav1.Object, finalizer string) (finalizersUpdated bool) {
	f := o.GetFinalizers()
	length := len(f)

	index := 0
	for i := 0; i < length; i++ {
		if f[i] == finalizer {
			continue
		}
		f[index] = f[i]
		index++
	}
	o.SetFinalizers(f[:index])
	return length != index
}

// ContainsFinalizer checks an Object that the provided finalizer is present.
func ContainsFinalizer(o metav1.Object, finalizer string) bool {
	for _, f := range o.GetFinalizers() {
		if f == finalizer {
			return true
		}
	}
	return false
}
