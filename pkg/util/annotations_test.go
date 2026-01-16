package util

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestFilterAndMergeAnnotations(t *testing.T) {
	tests := []struct {
		name   string
		source map[string]string
		target map[string]string
		want   map[string]string
	}{
		{
			name: "Empty",
			want: map[string]string{},
		},
		{
			name: "Unfiltered",
			source: map[string]string{
				"ignore.io": "test",
			},
			target: map[string]string{
				"result.io": "pass",
			},
			want: map[string]string{
				"result.io": "pass",
			},
		},
		{
			name: "Filtered",
			source: map[string]string{
				"cdi.kubevirt.io/externalPopulation": "true",
			},
			target: map[string]string{
				"result.io": "pass",
			},
			want: map[string]string{
				"result.io":                          "pass",
				"cdi.kubevirt.io/externalPopulation": "true",
			},
		},
		{
			name: "FilteredIntoEmpty",
			source: map[string]string{
				"cdi.kubevirt.io/externalPopulation": "true",
			},
			want: map[string]string{
				"cdi.kubevirt.io/externalPopulation": "true",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := FilterAndMergeAnnotations(test.source, test.target, "")
			if diff := cmp.Diff(result, test.want); diff != "" {
				t.Errorf("MergeAnnotations(): returned unexpected diff (+got, -want):\n%v", diff)
			}
		})
	}
}
