package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestParseStorageToGi(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", "0"},
		{"10Gi", "10Gi"},
		{"10G", "10Gi"},
		{"1Ti", "1024Gi"},
		{"2T", "2048Gi"},
		{"1024Mi", "1.0Gi"},
		{"512Mi", "0.5Gi"},
		{"1048576Ki", "1.00Gi"},
		{"1073741824", "1.00Gi"}, // bare bytes
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := parseStorageToGi(tt.in); got != tt.want {
				t.Errorf("parseStorageToGi(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseSizeFloat(t *testing.T) {
	tests := []struct {
		in   string
		want float64
	}{
		{"10Gi", 10},
		{"0.5Gi", 0.5},
		{"0", 0},
		{"1024Gi", 1024},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := parseSizeFloat(tt.in); got != tt.want {
				t.Errorf("parseSizeFloat(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestYamlStr(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"simple", "simple"},
		{"with-dash", "with-dash"},
		{"has: colon", `"has: colon"`},
		{"a,b", `"a,b"`},
		{"two  spaces", `"two  spaces"`},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := yamlStr(tt.in); got != tt.want {
				t.Errorf("yamlStr(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// pvFixture mirrors how main() decodes a PV into a generic map via json.
func pvFixture(t *testing.T, raw string) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("bad fixture: %v", err)
	}
	return m
}

func TestGetStringValue(t *testing.T) {
	m := pvFixture(t, `{
		"metadata": {"name": "pv-1"},
		"spec": {"storageClassName": "gp3", "capacity": {"storage": "10Gi"}}
	}`)

	if got := getStringValue(m, "metadata", "name"); got != "pv-1" {
		t.Errorf("name = %q, want pv-1", got)
	}
	if got := getStringValue(m, "spec", "storageClassName"); got != "gp3" {
		t.Errorf("storageClassName = %q, want gp3", got)
	}
	if got := getStringValue(m, "spec", "missing"); got != "" {
		t.Errorf("missing key = %q, want empty", got)
	}
	if got := getStringValue(m, "nope", "nested"); got != "" {
		t.Errorf("missing parent = %q, want empty", got)
	}
}

func TestGetMapValue(t *testing.T) {
	m := pvFixture(t, `{"spec": {"capacity": {"storage": "10Gi"}}}`)

	capMap := getMapValue(m, "spec", "capacity")
	if capMap == nil {
		t.Fatal("getMapValue returned nil for spec.capacity")
	}
	if got := getStringFromMap(capMap, "storage"); got != "10Gi" {
		t.Errorf("storage = %q, want 10Gi", got)
	}
	if getMapValue(m, "spec", "capacity", "storage") != nil {
		t.Error("getMapValue should return nil when leaf is not a map")
	}
}

func TestGetArrayValue(t *testing.T) {
	m := pvFixture(t, `{"spec": {"values": ["a", "b"]}}`)
	spec := getMapValue(m, "spec")
	got := getArrayValue(spec, "values")
	want := []interface{}{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("getArrayValue() = %v, want %v", got, want)
	}
	if getArrayValue(spec, "missing") != nil {
		t.Error("getArrayValue should return nil for missing key")
	}
}
