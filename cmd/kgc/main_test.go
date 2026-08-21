package main

import (
	"reflect"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetPodStatus(t *testing.T) {
	now := metav1.Now()
	tests := []struct {
		name string
		pod  v1.Pod
		want string
	}{
		{
			name: "terminating",
			pod:  v1.Pod{ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now}},
			want: "Terminating",
		},
		{
			name: "status reason",
			pod:  v1.Pod{Status: v1.PodStatus{Reason: "Evicted"}},
			want: "Evicted",
		},
		{
			name: "waiting reason",
			pod: v1.Pod{Status: v1.PodStatus{
				ContainerStatuses: []v1.ContainerStatus{
					{State: v1.ContainerState{Waiting: &v1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}},
				},
			}},
			want: "CrashLoopBackOff",
		},
		{
			name: "terminated reason",
			pod: v1.Pod{Status: v1.PodStatus{
				ContainerStatuses: []v1.ContainerStatus{
					{State: v1.ContainerState{Terminated: &v1.ContainerStateTerminated{Reason: "Completed"}}},
				},
			}},
			want: "Completed",
		},
		{
			name: "falls back to phase",
			pod:  v1.Pod{Status: v1.PodStatus{Phase: v1.PodRunning}},
			want: "Running",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getPodStatus(tt.pod); got != tt.want {
				t.Errorf("getPodStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildRows(t *testing.T) {
	pod := v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "prod"},
		Spec: v1.PodSpec{Containers: []v1.Container{
			{Name: "app", Image: "nginx:1.27"},
			{Name: "sidecar", Image: "envoy:1.30"},
		}},
		Status: v1.PodStatus{
			Phase: v1.PodRunning,
			ContainerStatuses: []v1.ContainerStatus{
				{Name: "app", Ready: true, RestartCount: 2,
					LastTerminationState: v1.ContainerState{
						Terminated: &v1.ContainerStateTerminated{Reason: "OOMKilled"},
					}},
				{Name: "sidecar", Ready: false, RestartCount: 1},
			},
		},
	}

	rows := buildRows(pod)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}

	if !rows[0].firstInPod || rows[1].firstInPod {
		t.Errorf("firstInPod flags wrong: %v, %v", rows[0].firstInPod, rows[1].firstInPod)
	}
	// Restarts are totaled across containers and shared on every row.
	if rows[0].restarts != 3 || rows[1].restarts != 3 {
		t.Errorf("restarts = %d, %d; want 3, 3", rows[0].restarts, rows[1].restarts)
	}
	if rows[0].lastReason != "OOMKilled" {
		t.Errorf("rows[0].lastReason = %q, want OOMKilled", rows[0].lastReason)
	}
	if rows[1].lastReason != "" {
		t.Errorf("rows[1].lastReason = %q, want empty", rows[1].lastReason)
	}
	if !rows[0].ready || rows[1].ready {
		t.Errorf("ready flags wrong: %v, %v", rows[0].ready, rows[1].ready)
	}
}

func TestTableHeaders(t *testing.T) {
	tests := []struct {
		name          string
		allNamespaces bool
		showImage     bool
		want          []string
	}{
		{
			name: "default",
			want: []string{"POD", "CONTAINER", "READY", "RESTARTS", "AGE", "LAST RESTART REASON"},
		},
		{
			name:          "all namespaces",
			allNamespaces: true,
			want:          []string{"NAMESPACE", "POD", "CONTAINER", "READY", "RESTARTS", "AGE", "LAST RESTART REASON"},
		},
		{
			name:      "show image",
			showImage: true,
			want:      []string{"POD", "CONTAINER", "READY", "RESTARTS", "AGE", "LAST RESTART REASON", "IMAGE"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tableHeaders(tt.allNamespaces, tt.showImage)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("tableHeaders() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRowCols(t *testing.T) {
	r := containerRow{
		namespace: "prod", podName: "web", container: "app",
		ready: true, restarts: 4, age: "2d", lastReason: "Error",
		image: "nginx:1.27",
	}

	got := rowCols(r, true, true)
	want := []string{"prod", "web", "app", "true", "4", "2d", "Error", "nginx:1.27"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rowCols() = %v, want %v", got, want)
	}

	// Completed pods report "Completed" rather than false in the READY column.
	completed := containerRow{podName: "job", container: "run", ready: false, podStatus: "Completed"}
	if cols := rowCols(completed, false, false); cols[2] != "Completed" {
		t.Errorf("rowCols() ready col = %q, want Completed", cols[2])
	}
}
