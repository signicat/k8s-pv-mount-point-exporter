# k8s-pv-mount-point-exporter

Export useful metadata about Kubernetes PersistentVolumes as Prometheus metrics.

PS: This is currently opinionated towards GKE, specifically regarding exporting `type` and `replication_type` parameters from the `pd.csi.storage.gke.io` Storage Provisioner.

## Installation

k8s-pv-mount-point-exporter ships as a helm chart that can be installed like this:

    helm repo add k8s-pv-mount-point-exporter 'https://raw.githubusercontent.com/signicat/k8s-pv-mount-point-exporter/main/chart/'
    helm repo update

    kubectl create namespace k8s-pv-mount-point-exporter
    helm upgrade --install --namespace=k8s-pv-mount-point-exporter k8s-pv-mount-point-exporter k8s-pv-mount-point-exporter/k8s-pv-mount-point-exporter

## Metrics exported

`persistentvolume_mount_point_info` - node mount points for PersistentVolumes:

```
persistentvolume_mount_point_info{
    app="k8s-pv-mount-point-exporter",
    device="/dev/sdb",
    persistentvolume="pvc-c76a723f-ceb4-45e3-b6ab-46a9c13b7d8c",
    volumename="pvc-c76a723f-ceb4-45e3-b6ab-46a9c13b7d8c",
    node="gke-bla"
} 1

```

> PS: PV name is exported in both `persistentvolume` and `volumename` for easier joining with metrics that may use either label names.

`storageclass_parameters` - names and selected parameters (`type` and `replication_type`) from StorageClasses:

```
storageclass_parameters{
  app="k8s-pv-mount-point-exporter",
  storageclass="balanced-zonal",
  pd_type="pd-balanced-zonal-pd",
  type="pd-balanced",
  replication_type="zonal-pd",
  node="macbook"
} 1
```

> The `type` and `replication_type` labels are meant to be used in conjunction with static GCP Disk metrics from https://github.com/StianOvrevage/gcp-info-metrics/blob/main/metrics_gcp_info_pd.txt
> This is useful to group PVs of different StorageClass but the same type. Since GCP instance/node disk limits are calculated based on total capacity of all disks (GB) attached to one instance/node _per persistent disk type_.

`rr_disk_info` - Optional metric joining lots of useful data:

```
rr_disk_info{
  device="sdb",
  label_cloud_google_com_gke_nodepool="default",
  label_cloud_google_com_gke_provisioning="spot",
  label_cloud_google_com_machine_family="n2d",
  label_node_kubernetes_io_instance_type="n2d-standard-8",
  label_topology_gke_io_zone="europe-west4-b",
  namespace="prometheus",
  node="gke-default-56e214a6-sjvg",
  pd_type="pd-balanced-regional-pd",
  persistentvolume="pvc-7ea34ec4-f5b4-4d9f-8756-b6da9d269de0",
  persistentvolumeclaim="prometheus-1",
  pod="prometheus-1",
  replication_type="regional-pd",
  storageclass="balanced-regional-cmek",
  type="pd-balanced",
  volume="data",
  volumename="pvc-7ea34ec4-f5b4-4d9f-8756-b6da9d269de0"
} 1
```

> PS: Requires VictoriaMetrics and enabling of VMRule in helm chart with `vmrule.enabled=true`.

Optionally create a Victoria Metrics Recording Rule (VMRule) that combines labels from these metrics:
- kube_pod_spec_volumes_persistentvolumeclaims_info (kube-state-metrics)
- kube_persistentvolumeclaim_info (kube-state-metrics)
- kube_node_labels (kube-state-metrics)
- persistentvolume_mount_point_info (k8s-pv-mount-point-exporter)
- storageclass_parameters (k8s-pv-mount-point-exporter)

`node_info_cpu_count` - node name and CPU count

Intended as a convenience for using with https://github.com/StianOvrevage/gcp-info-metrics/blob/main/metrics_gcp_info_pd.txt to query for instance maximum performance numbers that are determined by instance type and CPU count.

## How it works

### persistentvolume_mount_point_info

Mounts `/var/lib/kubelet/pods/` of the host into the Pod using `hostPath`.

> PS: Due to https://kubernetes.io/blog/2024/04/23/recursive-read-only-mounts/#new-mount-option-recursivereadonly . All volumes/disks on the node will be mounted and writable by k8s-pv-mount-point-exporter until `recursiveReadOnly` is available and enabled.

Runs a `golang` program that every 10 seconds:
- Reads `/proc/self/mounts` and uses regexp to extract the device (`/dev/sdc`) and PV name (`pvc-d10726c2-8eb7-4c66-80ca-9f3c4239cb40`).
- Updates metrics cache that is scraped by Prometheus-like systems.

### storageclass_parameters

- Calls Kubernetes API every 5 minutes to get all StorageClasses.
- Updates the metrics cache for exporting `storageclass_parameters` containing labels `storageclass` and `type`.

## node_info_cpu_count

- Calls Kubernetes API on startup to get all Node CPU counts

## Configuration

Required environment variables (these are configured by default if you are using the helm chart):
- `K8S_NODE_NAME` - Hostname of node for metric label purposes
- `METRICS_LISTEN` - Address to run metrics-server on. For example `:8088` or `localhost:8088`.
- `MOCK_MOUNT_OUTPUT` - Use contents of `testing/mount.txt` instead of output from `mount`. Useful for testing.

## Security

PS: This program has write access to all mounted disks in all Pods due to https://kubernetes.io/blog/2024/04/23/recursive-read-only-mounts/#new-mount-option-recursivereadonly

This will hopefully be enabled by default in some future Kubernetes version and prevent k8s-pv-mount-point-exporter from having write access.
