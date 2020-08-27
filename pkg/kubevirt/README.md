# CSI KubeVirt driver


## Kubernetes
### Requirements

The folllowing feature gates and runtime config have to be enabled to deploy the driver

```
FEATURE_GATES=CSIPersistentVolume=true,MountPropagation=true
RUNTIME_CONFIG="storage.k8s.io/v1alpha1=true"
```

## Using CSC tool

### Build kubevirt plugin
```
$ make
```

### Start KubeVirt driver
```
$ sudo ./_output/kubevirtplugin --endpoint tcp://127.0.0.1:10000 --nodeid CSINode -v=5
```

## Test
Get ```csc``` tool from https://github.com/rexray/gocsi/tree/master/csc

#### Get plugin info
```
$ csc identity plugin-info --endpoint tcp://127.0.0.1:10000
"Kubevirt"	"0.0.0"
```

#### NodePublish a volume
```
$ csc node publish --endpoint tcp://127.0.0.1:10000 <params> testvol
testvol
```

#### NodeUnpublish a volume
```
$ csc node unpublish --endpoint tcp://127.0.0.1:10000 <params> testvol
testvol
```

#### Get NodeID
```
$ csc node get-id --endpoint tcp://127.0.0.1:10000
CSINode
```

