package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"sort"
	"strings"
	"sync"

	"github.com/gookit/color"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var version = "dev" // overridden by ldflags at release build time; falls back to module version via ReadBuildInfo

func init() {
	if version == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok {
			v := info.Main.Version
			if v != "" && v != "(devel)" {
				version = strings.TrimPrefix(v, "v")
			}
		}
	}
}

type PVInfo struct {
	Namespace     string
	Zone          string
	SizeGi        string
	StorageClass  string
	PVCName       string
	PVName        string
	Pod           string
	ReclaimPolicy string
}

func printCompletions(shell string) {
	switch shell {
	case "bash":
		fmt.Print(`_kgpv_completions() {
    local cur="${COMP_WORDS[COMP_CWORD]}"
    local opts="--hide-pod --show-pv-name --show-reclaim-policy --show-all-fields --sort --output --completions -v --version"
    COMPREPLY=($(compgen -W "${opts}" -- "${cur}"))
}
complete -F _kgpv_completions kgpv
`)
	case "zsh":
		fmt.Print(`#compdef kgpv
_kgpv() {
    _arguments \
        '--hide-pod[Hide pod column]' \
        '--show-pv-name[Show PV name column]' \
        '--show-reclaim-policy[Show reclaim policy column]' \
        '--show-all-fields[Show all columns]' \
        '--sort[Sort by column]:column:(namespace pod zone size pvc pv)' \
        '--output[Output format]:format:(csv json yaml)' \
        '--completions[Print shell completion script]:shell:(bash zsh)' \
        '(-v --version)'{-v,--version}'[Print version and exit]'
}
_kgpv
`)
	default:
		fmt.Fprintf(os.Stderr, "unknown shell %q: use bash or zsh\n", shell)
		os.Exit(1)
	}
}

func main() {
	// Parse flags
	hidePod := flag.Bool("hide-pod", false, "Hide pod column")
	showPVName := flag.Bool("show-pv-name", false, "Show PV name column")
	showReclaimPolicy := flag.Bool("show-reclaim-policy", false, "Show reclaim policy column")
	allFields := flag.Bool("show-all-fields", false, "Show all columns")
	sortBy := flag.String("sort", "namespace", "Sort by column: namespace, pod, zone, size, pvc, pv")
	outputFormat := flag.String("output", "", "Output format: csv, json, yaml")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.BoolVar(showVersion, "v", false, "Print version and exit")
	completionsFlag := flag.String("completions", "", "Print shell completion script (bash or zsh)")
	flag.Parse()

	if *completionsFlag != "" {
		printCompletions(*completionsFlag)
		return
	}

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	// --show-all-fields enables every optional column
	if *allFields {
		*hidePod = false
		*showPVName = true
		*showReclaimPolicy = true
	}

	showPod := !*hidePod

	// Color definitions
	headerColor := color.New(color.FgCyan, color.OpBold)
	pvColor := color.New(color.FgWhite)
	pvcColor := color.New(color.FgWhite)
	nsColor := color.New(color.FgWhite)
	podColor := color.New(color.FgWhite)
	zonePalette := []color.Style{
		{color.FgMagenta},
		{color.FgYellow},
		{color.FgGreen},
		{color.FgBlue},
	}
	sizeColor := color.New(color.FgCyan)
	unboundColor := color.New(color.FgLightRed, color.OpBold)
	reclaimColor := color.New(color.FgWhite)

	// Load kubeconfig
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	config, err := kubeConfig.ClientConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading kubeconfig: %v\n", err)
		os.Exit(1)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating Kubernetes client: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// Get all PVs
	pvList, err := clientset.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing PVs: %v\n", err)
		os.Exit(1)
	}

	// Process PVs concurrently
	var wg sync.WaitGroup
	results := make(chan PVInfo, len(pvList.Items))

	// Pre-fetch all pods unless --hide-pod is used (single API call)
	var podsByNs map[string]map[string]string // namespace -> pvcName -> podName
	if showPod {
		podsByNs = make(map[string]map[string]string)

		// Single API call for all pods across all namespaces
		podList, err := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
		if err == nil {
			for _, pod := range podList.Items {
				ns := pod.Namespace
				if podsByNs[ns] == nil {
					podsByNs[ns] = make(map[string]string)
				}
				for _, volume := range pod.Spec.Volumes {
					if volume.PersistentVolumeClaim != nil {
						podsByNs[ns][volume.PersistentVolumeClaim.ClaimName] = pod.Name
					}
				}
			}
		}
	}

	for _, pv := range pvList.Items {
		wg.Add(1)
		go func(pv interface{}) {
			defer wg.Done()

			pvData, _ := json.Marshal(pv)
			var pvMap map[string]interface{}
			_ = json.Unmarshal(pvData, &pvMap)

			info := PVInfo{
				Namespace:    "null",
				Zone:         "UNKNOWN",
				SizeGi:       "0",
				StorageClass: "",
				PVCName:      "UNBOUND",
				PVName:       getStringValue(pvMap, "metadata", "name"),
				Pod:          "null",
			}

			// Get size in Gi
			if capacity := getMapValue(pvMap, "spec", "capacity"); capacity != nil {
				if storage := getStringFromMap(capacity, "storage"); storage != "" {
					info.SizeGi = parseStorageToGi(storage)
				}
			}

			// Get PVC info
			if claimRef := getMapValue(pvMap, "spec", "claimRef"); claimRef != nil {
				if name := getStringFromMap(claimRef, "name"); name != "" {
					info.PVCName = name
				}
				if ns := getStringFromMap(claimRef, "namespace"); ns != "" {
					info.Namespace = ns
				}
			}

			// Get storage class
			info.StorageClass = getStringValue(pvMap, "spec", "storageClassName")

			// Get reclaim policy
			info.ReclaimPolicy = getStringValue(pvMap, "spec", "persistentVolumeReclaimPolicy")

			// Get zone from node affinity
			if nodeAffinity := getMapValue(pvMap, "spec", "nodeAffinity"); nodeAffinity != nil {
				if required := getMapValue(nodeAffinity, "required"); required != nil {
					if terms := getArrayValue(required, "nodeSelectorTerms"); len(terms) > 0 {
						if term, ok := terms[0].(map[string]interface{}); ok {
							if exprs := getArrayValue(term, "matchExpressions"); len(exprs) > 0 {
								for _, expr := range exprs {
									if exprMap, ok := expr.(map[string]interface{}); ok {
										if key := getStringFromMap(exprMap, "key"); key == "topology.disk.csi.azure.com/zone" || key == "topology.kubernetes.io/zone" || key == "topology.gke.io/zone" {
											if values := getArrayValue(exprMap, "values"); len(values) > 0 {
												if zone, ok := values[0].(string); ok {
													info.Zone = zone
												}
											}
										}
									}
								}
							}
						}
					}
				}
			}

			// Fallback: check labels for zone
			if info.Zone == "UNKNOWN" {
				if labels := getMapValue(pvMap, "metadata", "labels"); labels != nil {
					if zone := getStringFromMap(labels, "topology.kubernetes.io/zone"); zone != "" {
						info.Zone = zone
					} else if zone := getStringFromMap(labels, "topology.gke.io/zone"); zone != "" {
						info.Zone = zone
					} else if zone := getStringFromMap(labels, "failure-domain.beta.kubernetes.io/zone"); zone != "" {
						info.Zone = zone
					}
				}
			}

			// Find pod using this PVC (unless --hide-pod)
			if showPod && info.PVCName != "--UNBOUND--" && info.Namespace != "--NONE--" {
				if nsPods, ok := podsByNs[info.Namespace]; ok {
					if podName, ok := nsPods[info.PVCName]; ok {
						info.Pod = podName
					}
				}
			}

			results <- info
		}(pv)
	}

	// Close results channel when all goroutines complete
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	var infos []PVInfo
	for info := range results {
		infos = append(infos, info)
	}

	// Sort by chosen column, tiebreak namespace → pod → pvc
	sort.Slice(infos, func(i, j int) bool {
		a, b := infos[i], infos[j]
		var primary bool
		var equal bool
		switch *sortBy {
		case "pod":
			primary, equal = a.Pod < b.Pod, a.Pod == b.Pod
		case "zone":
			primary, equal = a.Zone < b.Zone, a.Zone == b.Zone
		case "size":
			sa, sb := parseSizeFloat(a.SizeGi), parseSizeFloat(b.SizeGi)
			primary, equal = sa < sb, sa == sb
		case "pvc":
			primary, equal = a.PVCName < b.PVCName, a.PVCName == b.PVCName
		case "pv":
			primary, equal = a.PVName < b.PVName, a.PVName == b.PVName
		default: // "namespace"
			primary, equal = a.Namespace < b.Namespace, a.Namespace == b.Namespace
		}
		if !equal {
			return primary
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		if a.Pod != b.Pod {
			return a.Pod < b.Pod
		}
		return a.PVCName < b.PVCName
	})

	// Structured output formats
	switch *outputFormat {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(infos)
		return
	case "yaml":
		printYAMLOutput(infos)
		return
	case "csv":
		printCSVOutput(infos, showPod, *showPVName, *showReclaimPolicy)
		return
	}

	// Assign a stable color to each unique zone in sorted order
	zoneColors := make(map[string]color.Style)
	for _, info := range infos {
		if _, ok := zoneColors[info.Zone]; !ok {
			zoneColors[info.Zone] = zonePalette[len(zoneColors)%len(zonePalette)]
		}
	}

	// Calculate dynamic column widths
	maxNs := len("NAMESPACE")
	maxPod := len("POD")
	maxZone := len("ZONE")
	maxSize := len("SIZE")
	maxSC := len("STORAGE-CLASS")
	maxPVC := len("PVC")
	maxPV := len("PV NAME")
	maxReclaim := len("RECLAIM-POLICY")

	for _, info := range infos {
		if len(info.Namespace) > maxNs {
			maxNs = len(info.Namespace)
		}
		if showPod && len(info.Pod) > maxPod {
			maxPod = len(info.Pod)
		}
		if len(info.Zone) > maxZone {
			maxZone = len(info.Zone)
		}
		if len(info.SizeGi) > maxSize {
			maxSize = len(info.SizeGi)
		}
		if len(info.StorageClass) > maxSC {
			maxSC = len(info.StorageClass)
		}
		if len(info.PVCName) > maxPVC {
			maxPVC = len(info.PVCName)
		}
		if *showPVName && len(info.PVName) > maxPV {
			maxPV = len(info.PVName)
		}
		if *showReclaimPolicy && len(info.ReclaimPolicy) > maxReclaim {
			maxReclaim = len(info.ReclaimPolicy)
		}
	}

	// Add padding
	maxPod += 2
	maxNs += 2
	maxZone += 2
	maxSize += 2
	maxSC += 2
	maxPVC += 2
	maxPV += 2
	maxReclaim += 2

	// Build and print header
	var hdr strings.Builder
	fmt.Fprintf(&hdr, "%-*s ", maxNs, "NAMESPACE")
	if showPod {
		fmt.Fprintf(&hdr, "%-*s ", maxPod, "POD")
	}
	fmt.Fprintf(&hdr, "%-*s ", maxZone, "ZONE")
	fmt.Fprintf(&hdr, "%-*s ", maxSize, "SIZE")
	fmt.Fprintf(&hdr, "%-*s ", maxSC, "STORAGE-CLASS")
	fmt.Fprintf(&hdr, "%-*s", maxPVC, "PVC")
	if *showPVName {
		fmt.Fprintf(&hdr, " %-*s", maxPV, "PV NAME")
	}
	if *showReclaimPolicy {
		fmt.Fprintf(&hdr, " %-*s", maxReclaim, "RECLAIM-POLICY")
	}
	headerColor.Println(hdr.String())

	// Print rows
	for _, info := range infos {
		if info.Namespace == "null" {
			unboundColor.Printf("%-*s ", maxNs, info.Namespace)
		} else {
			nsColor.Printf("%-*s ", maxNs, info.Namespace)
		}

		if showPod {
			if info.Pod == "null" {
				unboundColor.Printf("%-*s ", maxPod, info.Pod)
			} else {
				podColor.Printf("%-*s ", maxPod, info.Pod)
			}
		}

		if info.Zone == "UNKNOWN" {
			unboundColor.Printf("%-*s ", maxZone, info.Zone)
		} else {
			zc := zoneColors[info.Zone]
			zc.Printf("%-*s ", maxZone, info.Zone)
		}

		sizeColor.Printf("%-*s ", maxSize, info.SizeGi)

		pvColor.Printf("%-*s ", maxSC, info.StorageClass)

		if info.PVCName == "UNBOUND" {
			unboundColor.Printf("%-*s", maxPVC, info.PVCName)
		} else {
			pvcColor.Printf("%-*s", maxPVC, info.PVCName)
		}

		if *showPVName {
			fmt.Print(" ")
			pvColor.Printf("%-*s", maxPV, info.PVName)
		}

		if *showReclaimPolicy {
			fmt.Print(" ")
			reclaimColor.Printf("%-*s", maxReclaim, info.ReclaimPolicy)
		}

		fmt.Println()
	}
}

func printYAMLOutput(infos []PVInfo) {
	fmt.Println("---")
	for _, info := range infos {
		fmt.Printf("- namespace: %s\n", yamlStr(info.Namespace))
		fmt.Printf("  pod: %s\n", yamlStr(info.Pod))
		fmt.Printf("  zone: %s\n", yamlStr(info.Zone))
		fmt.Printf("  size: %s\n", yamlStr(info.SizeGi))
		fmt.Printf("  storageClass: %s\n", yamlStr(info.StorageClass))
		fmt.Printf("  pvc: %s\n", yamlStr(info.PVCName))
		fmt.Printf("  pvName: %s\n", yamlStr(info.PVName))
		fmt.Printf("  reclaimPolicy: %s\n", yamlStr(info.ReclaimPolicy))
	}
}

func yamlStr(s string) string {
	if strings.ContainsAny(s, `:{}[]|>&*!,"`) || strings.Contains(s, "  ") {
		return fmt.Sprintf("%q", s)
	}
	return s
}

func printCSVOutput(infos []PVInfo, showPod, showPVName, showReclaimPolicy bool) {
	w := csv.NewWriter(os.Stdout)
	defer w.Flush()

	header := []string{"NAMESPACE"}
	if showPod {
		header = append(header, "POD")
	}
	header = append(header, "ZONE", "SIZE", "STORAGE-CLASS", "PVC")
	if showPVName {
		header = append(header, "PV NAME")
	}
	if showReclaimPolicy {
		header = append(header, "RECLAIM-POLICY")
	}
	_ = w.Write(header)

	for _, info := range infos {
		row := []string{info.Namespace}
		if showPod {
			row = append(row, info.Pod)
		}
		row = append(row, info.Zone, info.SizeGi, info.StorageClass, info.PVCName)
		if showPVName {
			row = append(row, info.PVName)
		}
		if showReclaimPolicy {
			row = append(row, info.ReclaimPolicy)
		}
		_ = w.Write(row)
	}
}

// parseSizeFloat parses a formatted SizeGi string (e.g. "10Gi") back to a float for sorting.
func parseSizeFloat(s string) float64 {
	var v float64
	_, _ = fmt.Sscanf(s, "%f", &v)
	return v
}

// parseStorageToGi converts Kubernetes storage format to Gi
func parseStorageToGi(storage string) string {
	if storage == "" {
		return "0"
	}

	// Handle common formats: 10Gi, 10G, 10240Mi, 10485760Ki, etc.
	var value float64
	var unit string

	_, _ = fmt.Sscanf(storage, "%f%s", &value, &unit)

	switch unit {
	case "Gi", "G":
		return fmt.Sprintf("%.0fGi", value)
	case "Mi", "M":
		return fmt.Sprintf("%.1fGi", value/1024)
	case "Ki", "K":
		return fmt.Sprintf("%.2fGi", value/1024/1024)
	case "Ti", "T":
		return fmt.Sprintf("%.0fGi", value*1024)
	default:
		// Assume bytes
		return fmt.Sprintf("%.2fGi", value/1024/1024/1024)
	}
}

// Helper functions to safely navigate nested maps
func getMapValue(m map[string]interface{}, keys ...string) map[string]interface{} {
	current := m
	for _, key := range keys {
		if val, ok := current[key]; ok {
			if mapVal, ok := val.(map[string]interface{}); ok {
				current = mapVal
			} else {
				return nil
			}
		} else {
			return nil
		}
	}
	return current
}

func getStringValue(m map[string]interface{}, keys ...string) string {
	if len(keys) == 0 {
		return ""
	}
	lastKey := keys[len(keys)-1]
	parent := m
	if len(keys) > 1 {
		parent = getMapValue(m, keys[:len(keys)-1]...)
		if parent == nil {
			return ""
		}
	}
	if val, ok := parent[lastKey]; ok {
		if strVal, ok := val.(string); ok {
			return strVal
		}
	}
	return ""
}

func getStringFromMap(m map[string]interface{}, key string) string {
	if val, ok := m[key]; ok {
		if strVal, ok := val.(string); ok {
			return strVal
		}
	}
	return ""
}

func getArrayValue(m map[string]interface{}, key string) []interface{} {
	if val, ok := m[key]; ok {
		if arrVal, ok := val.([]interface{}); ok {
			return arrVal
		}
	}
	return nil
}

// Made with Bob
