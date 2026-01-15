package util

import (
	"bufio"
	"os"
)

// FilterAndMergeAnnotations will take a source and a target annotations. It will
// get all values for the annotations in filterAnnotations, and place the value
// in target.
func FilterAndMergeAnnotations(source, target map[string]string, annotationsAllowlistPath string) map[string]string {
	if target == nil {
		target = map[string]string{}
	}
	for _, annotation := range readAllowedAnnotations(annotationsAllowlistPath) {
		if val, ok := source[annotation]; ok {
			target[annotation] = val
		}
	}
	return target
}

func readAllowedAnnotations(annotationsAllowlistPath string) []string {
	if annotationsAllowlistPath == "" {
		return []string{}
	}

	allowlist, err := os.Open(annotationsAllowlistPath)
	if err != nil {
		return []string{}
	}
	defer allowlist.Close()

	var annotations []string
	scanner := bufio.NewScanner(allowlist)
	for scanner.Scan() {
		annotations = append(annotations, scanner.Text())
	}
	return annotations
}
