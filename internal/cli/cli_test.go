package cli

import (
	"bytes"
	"strings"
	"testing"

	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

func TestParseSize(t *testing.T) {
	cases := []struct {
		in   string
		want uint64
		err  bool
	}{
		{"512MiB", 512 << 20, false},
		{"2GiB", 2 << 30, false},
		{"1073741824", 1 << 30, false},
		{"abc", 0, true},
		{"GiB", 0, true},
	}
	for _, c := range cases {
		got, err := parseSize(c.in)
		if c.err != (err != nil) || got != c.want {
			t.Errorf("parseSize(%q) = %d, %v; want %d, err=%v", c.in, got, err, c.want, c.err)
		}
	}
}

func TestPrintVMsRendering(t *testing.T) {
	var buf bytes.Buffer
	printVMs(&buf, &vmcv1.VirtualMachine{
		Meta: &vmcv1.ResourceMeta{Name: "web-1", DesiredRevision: 2},
		Spec: &vmcv1.VmSpec{Cpus: 2, MemoryBytes: 2 << 30, Image: "ubuntu-24.04"},
		Status: &vmcv1.VmStatus{
			Phase: vmcv1.Phase_PHASE_RUNNING, Node: "node-a",
			PlacementEpoch: 1, AppliedRevision: 2,
		},
	})
	out := buf.String()
	for _, want := range []string{"web-1", "RUNNING", "node-a", "2GiB", "2/2"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPrintOperationRendering(t *testing.T) {
	var buf bytes.Buffer
	printOperation(&buf, &vmcv1.Operation{
		Id: "0f0e0d0c", Verb: vmcv1.Verb_VERB_CREATE,
		ResourceType: "vm", ResourceName: "web-1",
		State: vmcv1.OperationState_OPERATION_STATE_PENDING, TargetRevision: 1,
	})
	out := buf.String()
	for _, want := range []string{"CREATE", "vm/web-1", "PENDING"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestCommandTreeShape(t *testing.T) {
	root := New(new(bytes.Buffer))
	for _, path := range [][]string{
		{"create"}, {"get"}, {"list"}, {"delete"}, {"op", "wait"}, {"op", "get"},
	} {
		cmd, _, err := root.Find(path)
		if err != nil || cmd == nil {
			t.Errorf("command %v not found: %v", path, err)
		}
	}
}
