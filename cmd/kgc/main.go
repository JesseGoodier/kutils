package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/gookit/color"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/duration"
	"k8s.io/apimachinery/pkg/watch"
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

func die(msg string, err error) {
	fmt.Fprintf(os.Stderr, "error: %s: %v\n", msg, err)
	os.Exit(1)
}

type containerRow struct {
	namespace  string
	podName    string
	firstInPod bool // true only for the first container row of a pod
	container  string
	ready      bool
	podStatus  string // used for color logic
	restarts   int    // total for pod, shared across all container rows
	age        string
	lastReason string // per-container last termination reason
	image      string
}

func printUsage(kubeconfigDefault string) {
	title := color.Style{color.Bold, color.FgWhite}
	section := color.Style{color.Bold, color.FgCyan}
	flagName := color.FgCyan
	desc := color.FgWhite
	subtle := color.FgGray

	title.Println("Kubernetes Get Pod Status (kgc)")
	subtle.Printf("  version %s\n", version)
	fmt.Println()

	section.Println("USAGE")
	fmt.Println("  kgc [flags]")
	fmt.Println()

	section.Println("FLAGS")

	row := func(names, argument, description, def string) {
		nameCol := flagName.Sprint(names)
		argCol := ""
		if argument != "" {
			argCol = subtle.Sprintf(" <%s>", argument)
		}
		defCol := ""
		if def != "" {
			defCol = subtle.Sprintf("  (default: %s)", def)
		}
		fmt.Printf("  %-38s %s%s\n", nameCol+argCol, desc.Sprint(description), defCol)
	}

	row("-n, --namespace", "namespace", "Namespace to list pods in.", "current context")
	row("-A", "", "List pods across all namespaces.", "")
	row("-w, --watch", "", "Watch for pod changes.", "")
	row("-r, --has-restarts", "", "Only show pods with restarts.", "")
	row("--show-image", "", "Show container image column.", "")
	row("--kubeconfig", "path", "Path to kubeconfig file.", kubeconfigDefault)
	row("--completions", "shell", "Print shell completion script (bash or zsh).", "")
	row("-v, --version", "", "Print version and exit.", "")
	row("-h, --help", "", "Show this help message.", "")
	fmt.Println()
}

func printCompletions(shell string) {
	switch shell {
	case "bash":
		fmt.Print(`_kgc_completions() {
    local cur="${COMP_WORDS[COMP_CWORD]}"
    local opts="-n --namespace -A -w --watch -r --has-restarts --show-image --kubeconfig --completions -v --version -h --help"
    COMPREPLY=($(compgen -W "${opts}" -- "${cur}"))
}
complete -F _kgc_completions kgc
`)
	case "zsh":
		fmt.Print(`#compdef kgc
_kgc() {
    _arguments \
        '(-n --namespace)'{-n,--namespace}'[Namespace to list pods in]:namespace:' \
        '-A[List pods across all namespaces]' \
        '(-w --watch)'{-w,--watch}'[Watch for pod changes]' \
        '(-r --has-restarts)'{-r,--has-restarts}'[Only show pods with restarts]' \
        '--show-image[Show container image column]' \
        '--kubeconfig[Path to kubeconfig file]:file:_files' \
        '--completions[Print shell completion script]:shell:(bash zsh)' \
        '(-v --version)'{-v,--version}'[Print version and exit]' \
        '(-h --help)'{-h,--help}'[Show this help message]'
}
_kgc
`)
	default:
		fmt.Fprintf(os.Stderr, "unknown shell %q: use bash or zsh\n", shell)
		os.Exit(1)
	}
}

func main() {
	var kubeconfig *string
	kubeconfigDefault := "~/.kube/config"
	if env := os.Getenv("KUBECONFIG"); env != "" {
		kubeconfig = flag.String("kubeconfig", env, "")
		kubeconfigDefault = "$KUBECONFIG"
	} else if home, err := os.UserHomeDir(); err == nil {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "")
		kubeconfigDefault = ""
	}

	namespace := flag.String("n", "", "")
	flag.StringVar(namespace, "namespace", "", "")
	allNamespaces := flag.Bool("A", false, "")
	watchFlag := flag.Bool("w", false, "")
	flag.BoolVar(watchFlag, "watch", false, "")
	hasRestartsFlag := flag.Bool("has-restarts", false, "")
	flag.BoolVar(hasRestartsFlag, "r", false, "")
	showImage := flag.Bool("show-image", false, "")
	versionFlag := flag.Bool("version", false, "")
	flag.BoolVar(versionFlag, "v", false, "")

	completionsFlag := flag.String("completions", "", "")

	flag.Usage = func() { printUsage(kubeconfigDefault) }
	flag.Parse()

	if *completionsFlag != "" {
		printCompletions(*completionsFlag)
		return
	}

	if *versionFlag {
		fmt.Println("kgc", version)
		return
	}

	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		die("loading kubeconfig", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		die("creating Kubernetes client", err)
	}

	targetNamespace := *namespace
	if *allNamespaces {
		targetNamespace = ""
	} else if targetNamespace == "" {
		clientCfg, err := clientcmd.NewDefaultClientConfigLoadingRules().Load()
		if err != nil {
			die("loading kubeconfig rules", err)
		}
		currentContext := clientCfg.Contexts[clientCfg.CurrentContext]
		if currentContext != nil {
			targetNamespace = currentContext.Namespace
		}
		if targetNamespace == "" {
			targetNamespace = "default"
		}
	}

	pods, err := clientset.CoreV1().Pods(targetNamespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		die("listing pods", err)
	}

	var rows []containerRow
	for _, pod := range pods.Items {
		podRows := buildRows(pod)
		if *hasRestartsFlag && podRows[0].restarts == 0 {
			continue
		}
		rows = append(rows, podRows...)
	}

	widths := printTable(rows, *allNamespaces, *showImage)

	if !*watchFlag {
		return
	}

	watcher, err := clientset.CoreV1().Pods(targetNamespace).Watch(context.TODO(), metav1.ListOptions{
		ResourceVersion: pods.ResourceVersion,
	})
	if err != nil {
		die("starting watch", err)
	}
	defer watcher.Stop()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-sig:
			return
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return
			}
			pod, ok := event.Object.(*v1.Pod)
			if !ok {
				continue
			}
			podRows := buildRows(*pod)
			if *hasRestartsFlag && podRows[0].restarts == 0 {
				continue
			}
			// Expand column widths if this pod is wider than what we've seen.
			for _, r := range podRows {
				for i, c := range rowCols(r, *allNamespaces, *showImage) {
					if len(c) > widths[i] {
						widths[i] = len(c)
					}
				}
			}
			deleted := event.Type == watch.Deleted
			for _, r := range podRows {
				printRow(r, widths, *allNamespaces, *showImage, deleted)
			}
		}
	}
}

// printTable renders the full table and returns the computed column widths.
func printTable(rows []containerRow, allNamespaces bool, showImage bool) []int {
	const minPad = 2

	headers := tableHeaders(allNamespaces, showImage)
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, r := range rows {
		for i, c := range rowCols(r, allNamespaces, showImage) {
			if len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
	}

	pad := func(s string, w int) string {
		return s + strings.Repeat(" ", w-len(s))
	}

	// header
	var hb strings.Builder
	for i, h := range headers {
		if i < len(headers)-1 {
			hb.WriteString(pad(h, widths[i]+minPad))
		} else {
			hb.WriteString(h)
		}
	}
	color.Style{color.Bold, color.FgWhite}.Println(hb.String())

	for _, r := range rows {
		printRow(r, widths, allNamespaces, showImage, false)
	}

	return widths
}

func printRow(r containerRow, widths []int, allNamespaces bool, showImage bool, deleted bool) {
	const minPad = 2
	headers := tableHeaders(allNamespaces, showImage)
	cols := rowCols(r, allNamespaces, showImage)

	pad := func(s string, w int) string {
		return s + strings.Repeat(" ", w-len(s))
	}

	var lb strings.Builder
	for i, raw := range cols {
		isLast := i == len(cols)-1
		var padded string
		if isLast {
			padded = raw
		} else {
			padded = pad(raw, widths[i]+minPad)
		}

		var segment string
		if deleted {
			segment = color.FgGray.Sprint(padded)
			lb.WriteString(segment)
			continue
		}

		switch headers[i] {
		case "NAMESPACE":
			if r.firstInPod {
				segment = color.FgWhite.Sprint(padded)
			} else {
				segment = color.FgGray.Sprint(padded)
			}
		case "POD":
			if r.firstInPod {
				segment = color.FgWhite.Sprint(padded)
			} else {
				segment = color.FgGray.Sprint(padded)
			}
		case "CONTAINER":
			segment = color.FgCyan.Sprint(padded)
		case "READY":
			if r.ready || r.podStatus == "Completed" {
				segment = color.FgGreen.Sprint(padded)
			} else {
				segment = color.FgRed.Sprint(padded)
			}
		case "RESTARTS", "AGE":
			segment = color.FgWhite.Sprint(padded)
		case "LAST RESTART REASON":
			segment = colorLastReason(padded, r.lastReason)
		case "IMAGE":
			segment = color.FgGray.Sprint(padded)
		default:
			segment = padded
		}
		lb.WriteString(segment)
	}
	fmt.Println(lb.String())
}

func tableHeaders(allNamespaces bool, showImage bool) []string {
	cols := []string{"POD", "CONTAINER", "READY", "RESTARTS", "AGE", "LAST RESTART REASON"}
	if showImage {
		cols = append(cols, "IMAGE")
	}
	if allNamespaces {
		return append([]string{"NAMESPACE"}, cols...)
	}
	return cols
}

func buildRows(pod v1.Pod) []containerRow {
	// Calculate total pod restarts (shared across all container rows)
	totalRestarts := 0
	for _, cs := range pod.Status.ContainerStatuses {
		totalRestarts += int(cs.RestartCount)
	}

	podStatus := getPodStatus(pod)
	age := duration.HumanDuration(time.Since(pod.CreationTimestamp.Time))

	var rows []containerRow
	for i, c := range pod.Spec.Containers {
		ready := false
		lastReason := ""

		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name == c.Name {
				ready = cs.Ready
				if cs.LastTerminationState.Terminated != nil &&
					cs.LastTerminationState.Terminated.Reason != "" {
					lastReason = cs.LastTerminationState.Terminated.Reason
				}
				break
			}
		}

		rows = append(rows, containerRow{
			namespace:  pod.Namespace,
			podName:    pod.Name,
			firstInPod: i == 0,
			container:  c.Name,
			ready:      ready,
			podStatus:  podStatus,
			restarts:   totalRestarts,
			age:        age,
			lastReason: lastReason,
			image:      c.Image,
		})
	}
	return rows
}

func rowCols(r containerRow, allNamespaces bool, showImage bool) []string {
	readyStr := "false"
	if r.ready {
		readyStr = "true"
	} else if r.podStatus == "Completed" {
		readyStr = "Completed"
	}
	cols := []string{r.podName, r.container, readyStr, fmt.Sprintf("%d", r.restarts), r.age, r.lastReason}
	if showImage {
		cols = append(cols, r.image)
	}
	if allNamespaces {
		return append([]string{r.namespace}, cols...)
	}
	return cols
}

func colorLastReason(padded, reason string) string {
	switch reason {
	case "OOMKilled", "Error", "ContainerCannotRun", "DeadlineExceeded":
		return color.FgRed.Sprint(padded)
	case "Completed":
		return color.FgGreen.Sprint(padded)
	case "":
		return color.FgWhite.Sprint(padded)
	default:
		return color.FgYellow.Sprint(padded)
	}
}

func getPodStatus(pod v1.Pod) string {
	if pod.DeletionTimestamp != nil {
		return "Terminating"
	}

	if pod.Status.Reason != "" {
		return pod.Status.Reason
	}

	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
			return cs.State.Waiting.Reason
		}
		if cs.State.Terminated != nil && cs.State.Terminated.Reason != "" {
			return cs.State.Terminated.Reason
		}
	}

	return string(pod.Status.Phase)
}
