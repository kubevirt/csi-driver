package util

type StorageClassEnforcement struct {
	AllowList []string `yaml:"allowList"`
	AllowAll bool `yaml:"allowAll"`
	AllowDefault bool `yaml:"allowDefault"`
}