# k8s-pv-mount-point-exporter

Exports node mount points for PersistentVolumes:

```
persistentvolume_mount_point_info{app="k8s-pv-mount-point-exporter", device="/dev/sdb", persistentvolume="pvc-c76a723f-ceb4-45e3-b6ab-46a9c13b7d8c", volumename="pvc-c76a723f-ceb4-45e3-b6ab-46a9c13b7d8c", node="gke-bla"} 1
persistentvolume_mount_point_info{app="k8s-pv-mount-point-exporter", device="/dev/sdc", persistentvolume="pvc-ca141f0d-a09f-47d0-ac99-e7c6623bc4fe", volumename="pvc-ca141f0d-a09f-47d0-ac99-e7c6623bc4fe", node="gke-bla"} 1
persistentvolume_mount_point_info{app="k8s-pv-mount-point-exporter", device="/dev/sdd", persistentvolume="pvc-29f836c8-61bc-45eb-83d3-d894beca98bc", volumename="pvc-29f836c8-61bc-45eb-83d3-d894beca98bc", node="gke-bla"} 1
```

> PS: PV name is exported in both `persistentvolume` and `volumename` for easier joining with metrics that may use either label names.

And `parameters.type` for StorageClasses:

```
storageclass_parameters{app="k8s-pv-mount-point-exporter", storageclass="balanced-cmek", pd_type="pd-balanced-zonal-pd", type="pd-balanced", replication_type="zonal-pd", node="macbook"} 1
storageclass_parameters{app="k8s-pv-mount-point-exporter", storageclass="balanced-regional-cmek", pd_type="pd-balanced-regional-pd", type="pd-balanced", replication_type="regional-pd", node="macbook"} 1
storageclass_parameters{app="k8s-pv-mount-point-exporter", storageclass="extreme-cmek", pd_type="pd-extreme-zonal-pd", type="pd-extreme", replication_type="zonal-pd", node="macbook"} 1
```

> The `pd_type` label is meant to the labels used in https://github.com/StianOvrevage/gcp-info-metrics/blob/main/metrics_gcp_info_pd.txt

> This is useful to group PVs of different StorageClass but the same type. Since GCP instance/node disk limits are calculated based on total capacity of all disks (GB) attached to one instance/node _per persistent disk type_.

Optionally creates a Victoria Metrics Recording Rule (VMRule) that combines labels from these metrics:
- kube_pod_spec_volumes_persistentvolumeclaims_info (kube-state-metrics)
- kube_persistentvolumeclaim_info (kube-state-metrics)
- kube_node_labels (kube-state-metrics)
- persistentvolume_mount_point_info (k8s-pv-mount-point-exporter)
- storageclass_parameters (k8s-pv-mount-point-exporter)

To create a new metric `rr_disk_info` with all relevant disk related labels for easier joining and aggregation later.

## How it works

### persistentvolume_mount_point_info

Mounts `/var/lib/kubelet/pods/` of the host into the Pod using `hostPath`.

> PS: Due to https://kubernetes.io/blog/2024/04/23/recursive-read-only-mounts/#new-mount-option-recursivereadonly . All volumes/disks on the node will be mounted and writable by k8s-pv-mount-point-exporter until `recursiveReadOnly` is available and enabled.

Runs a `golang` program that every 10 seconds:
- Executes `mount` and uses regexp to extract the device (`/dev/sdc`) and PV name (`pvc-d10726c2-8eb7-4c66-80ca-9f3c4239cb40`).
- Updates metrics cache that is scraped by Prometheus-like systems.

### storageclass_parameters

- Calls Kubernetes API every 5 minutes to get all StorageClasses.
- Updates the metrics cache for exporting `storageclass_parameters` containing labels `storageclass` and `type`.

## Configuration

Required environment variables:
- `K8S_NODE_NAME` - Hostname of node for metric label purposes
- `METRICS_LISTEN` - Address to run metrics-server on. For example `:8088` or `localhost:8088`.
- `MOCK_MOUNT_OUTPUT` - Use contents of `testing/mount.txt` instead of output from `mount`. Useful for testing.