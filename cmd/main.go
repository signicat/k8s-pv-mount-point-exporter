package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	vm "github.com/VictoriaMetrics/metrics"
	"github.com/gorilla/handlers"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	Version   string
	Revision  string
	Branch    string
	BuildTime string
)

var updateIntervalSecondsMountPoint int64 = 10
var updateIntervalSecondsStorageClasses int64 = 300

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if os.Getenv("METRICS_LISTEN") == "" || os.Getenv("K8S_NODE_NAME") == "" {
		log.Fatal("Environment variables METRICS_LISTEN and K8S_NODE_NAME must be set. Exiting.")
	}

	go serveMetrics(os.Getenv("METRICS_LISTEN"), Version, Revision, Branch, BuildTime)

	go setCPUCount()

	go updateMountPoints(ctx)

	go updateStorageClasses()

	<-ctx.Done()
}

type StorageClassInfo struct {
	Name            string
	PDType          string // Concatenation of type and replication-type
	Type            string // Comes from the "parameters.type" field in StorageClass
	ReplicationType string // Comes from "parameters.replication-type" field in StorageClass. Set to "zonal-pd" if not set.
}

func setCPUCount() {

	clientset, err := getClientSet()
	if err != nil {
		panic(err)
	}

	nodes, err := clientset.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		panic(fmt.Errorf("failed to list nodes: %w", err))
	}

	for _, node := range nodes.Items {
		cpuCapacity := node.Status.Capacity[corev1.ResourceCPU]

		fmt.Printf("Node: %s | CPU: %s\n", node.Name, cpuCapacity.String())

		vm.GetOrCreateGauge(fmt.Sprintf(`node_info_cpu_count{app="pv-mount-point-exporter", node="%s", cpus="%s"}`, sanitizeLabelValue(node.Name), cpuCapacity.String()), func() float64 {
			return float64(1)
		})
	}
}

func updateStorageClasses() {

	ourStorageClasses := make(map[string]StorageClassInfo)

	clientset, err := getClientSet()
	if err != nil {
		panic(err)
	}

	for {
		latestStorageClasses := make(map[string]StorageClassInfo)

		// Get all StorageClasses
		storageClasses, err := clientset.StorageV1().StorageClasses().List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			panic(err.Error())
		}

		for _, sc := range storageClasses.Items {
			sci := getCompositeName(sc)
			if sci.PDType == "" {
				continue
			}

			latestStorageClasses[sci.Name] = sci
			fmt.Printf("StorageClass: %s, GCP PD Type name: %s GCP Disk Type: %s GCP Replication Type: %s\n", sanitizeLabelValue(sci.Name), sanitizeLabelValue(sci.PDType), sanitizeLabelValue(sci.Type), sanitizeLabelValue(sci.ReplicationType))

			// Add gauges for previously unseen devices
			_, ok := ourStorageClasses[sc.Name]
			if !ok {

				ourStorageClasses[sc.Name] = sci
				vm.GetOrCreateGauge(fmt.Sprintf(`storageclass_parameters{app="pv-mount-point-exporter", storageclass="%s", pd_type="%s", type="%s", replication_type="%s", node="%s"}`, sanitizeLabelValue(sci.Name), sanitizeLabelValue(sci.PDType), sanitizeLabelValue(sci.Type), sanitizeLabelValue(sci.ReplicationType), sanitizeLabelValue(os.Getenv("K8S_NODE_NAME"))), func() float64 {
					return float64(1)
				})
			}
		}

		// Remove/unregister metrics for disks that are no longer present
		for storageClass, sci := range ourStorageClasses {
			_, ok := latestStorageClasses[sci.Name]
			if !ok {
				delete(ourStorageClasses, storageClass)
				vm.UnregisterMetric(fmt.Sprintf(`storageclass_parameters{app="pv-mount-point-exporter", storageclass="%s", pd_type="%s", type="%s", replication_type="%s", node="%s"}`, sanitizeLabelValue(sci.Name), sanitizeLabelValue(sci.PDType), sanitizeLabelValue(sci.Type), sanitizeLabelValue(sci.ReplicationType), sanitizeLabelValue(os.Getenv("K8S_NODE_NAME"))))
			}
		}

		time.Sleep(time.Second * time.Duration(updateIntervalSecondsStorageClasses))
	}
}

func updateMountPoints(ctx context.Context) {
	re := regexp.MustCompile(`^/dev/(\S+) .*/(pvc-[a-f0-9\-]+)/mount`)

	mountPointToPV := make(map[string]string)

	ticker := time.NewTicker(time.Second * time.Duration(updateIntervalSecondsMountPoint))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			latestMountPointToPV := make(map[string]string)

			var mounts []byte
			var err error

			if os.Getenv("MOCK_MOUNT_OUTPUT") != "" {
				fmt.Printf("Environment variable MOCK_MOUNT_OUTPUT is set. Using contents of testing/proc-self-mounts.txt instead of /proc/self/mounts\n")
				mounts, err = os.ReadFile("testing/proc-self-mounts.txt")
				if err != nil {
					log.Fatalf("Error reading test file: %v\n", err)
					return
				}
			} else {
				mounts, err = os.ReadFile("/proc/self/mounts")
				if err != nil {
					log.Fatalf("Error reading /proc/self/mounts: %v\n", err)
					return
				}
			}

			lines := strings.Split(string(mounts), "\n")
			for _, line := range lines {
				matches := re.FindStringSubmatch(line)

				if len(matches) >= 3 {
					device := matches[1]
					pvc := matches[2]

					latestMountPointToPV[device] = pvc

					// Add gauges for previously unseen devices
					_, ok := mountPointToPV[device]
					if !ok {
						mountPointToPV[device] = pvc
						vm.GetOrCreateGauge(fmt.Sprintf(`persistentvolume_mount_point_info{app="pv-mount-point-exporter", device="%s", persistentvolume="%s", volumename="%s", node="%s"}`, sanitizeLabelValue(device), sanitizeLabelValue(pvc), sanitizeLabelValue(pvc), sanitizeLabelValue(os.Getenv("K8S_NODE_NAME"))), func() float64 {
							return float64(1)
						})
					}

					fmt.Printf("Device: %s PVC: %s\n", device, pvc)
				}
			}

			// Remove/unregister metrics for disks that are no longer present
			for device, pvc := range mountPointToPV {
				_, ok := latestMountPointToPV[device]
				if !ok {
					delete(mountPointToPV, device)
					vm.UnregisterMetric(fmt.Sprintf(`persistentvolume_mount_point_info{app="pv-mount-point-exporter", device="%s", persistentvolume="%s", volumename="%s", node="%s"}`, sanitizeLabelValue(device), sanitizeLabelValue(pvc), sanitizeLabelValue(pvc), sanitizeLabelValue(os.Getenv("K8S_NODE_NAME"))))
				}
			}
		}
	}

}

func serveMetrics(metricsListen, version, revision, branch, buildTimeStr string) {
	buildTime, _ := time.Parse(time.RFC3339, buildTimeStr)

	vm.GetOrCreateGauge(fmt.Sprintf(`signicat_build_info{app="pv-mount-point-exporter", version="%s", revision="%s", branch="%s", buildtime="%v"}`, version, revision, branch, buildTime.Unix()), func() float64 {
		return float64(1)
	})

	vm.GetOrCreateGauge(fmt.Sprintf(`signicat_build_time{app="pv-mount-point-exporter", version="%s", revision="%s", branch="%s"}`, version, revision, branch), func() float64 {
		return float64(buildTime.Unix())
	})

	slog.Info("Starting metrics server", "listen", fmt.Sprintf("%v/metrics", metricsListen))

	http.HandleFunc("/metrics", func(w http.ResponseWriter, req *http.Request) {
		vm.WritePrometheus(w, true)
	})

	log.Fatal(http.ListenAndServe(metricsListen, handlers.LoggingHandler(os.Stdout, http.DefaultServeMux)))
}

func getClientSet() (*kubernetes.Clientset, error) {
	// Try in-cluster config first
	config, err := rest.InClusterConfig()
	if err != nil {
		// Fallback to kubeconfig
		kubeconfig := filepath.Join(homeDir(), ".kube", "config")
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create Kubernetes config: %v", err)
		}
	}
	return kubernetes.NewForConfig(config)
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // for Windows
}

// Return concatenation of pd_type (like "pd-ssd") and replication-type (like "zonal-pd") for a composite name (like "pd-ssd-zonal-pd")
func getCompositeName(sc storagev1.StorageClass) (sci StorageClassInfo) {

	sci.Name = sc.Name

	paramType, ok := sc.Parameters["type"]
	if !ok {
		return StorageClassInfo{}
	}
	sci.Type = paramType

	paramReplicationType, ok := sc.Parameters["replication-type"]
	if !ok {
		paramReplicationType = "zonal-pd"
	}
	sci.ReplicationType = paramReplicationType

	sci.PDType = fmt.Sprintf("%s-%s", paramType, paramReplicationType)

	return sci
}

func sanitizeLabelValue(s string) string {
	// strip control chars
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
