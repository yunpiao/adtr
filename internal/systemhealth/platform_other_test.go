//go:build !linux

package systemhealth

import "testing"

func TestNativeSamplingIsExplicitlyUnsupported(t *testing.T) {
	sampler, err := NewSampler(Config{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := sampler.Sample()
	for _, meta := range []Metadata{snapshot.CPU.Metadata, snapshot.Memory.Metadata, snapshot.Uptime.Metadata, snapshot.Load.Metadata, snapshot.Storage[0].Metadata} {
		if meta.Availability != Unsupported || meta.Reason != "unsupported_platform" {
			t.Fatalf("native non-Linux support was fabricated: %+v", meta)
		}
	}
}
