cat << EOF >> /host/etc/containerd/config.toml
  [plugins."io.containerd.grpc.v1.cri".registry]
    [plugins."io.containerd.grpc.v1.cri".registry.mirrors]
      [plugins."io.containerd.grpc.v1.cri".registry.mirrors."192.168.66.2:5000"]
        endpoint = ["http://192.168.66.2:5000"]
    [plugins."io.containerd.grpc.v1.cri".registry.configs]
      [plugins."io.containerd.grpc.v1.cri".registry.configs."192.168.66.2:5000".tls]
        insecure_skip_verify = true
EOF


