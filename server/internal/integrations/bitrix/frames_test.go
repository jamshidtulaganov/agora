package bitrix

import (
	"reflect"
	"strings"
	"testing"
)

func TestIsVideo(t *testing.T) {
	cases := []struct {
		name string
		file string
		ct   string
		want bool
	}{
		{"mp4 ext", "rec.mp4", "", true},
		{"mov ext upper", "REC.MOV", "", true},
		{"webm ext", "clip.webm", "", true},
		{"content-type video", "blob", "video/mp4", true},
		{"content-type video webm", "x", "video/webm", true},
		{"png not video", "shot.png", "image/png", false},
		{"no ext no ct", "file", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsVideo(tc.file, tc.ct); got != tc.want {
				t.Errorf("IsVideo(%q,%q) = %v, want %v", tc.file, tc.ct, got, tc.want)
			}
		})
	}
}

func TestSceneDetectArgs(t *testing.T) {
	args := SceneDetectArgs("/tmp/in.mp4", "/tmp/out_%03d.jpg", 20, 0.30)
	want := []string{
		"-y",
		"-i", "/tmp/in.mp4",
		"-vf", "select='gt(scene,0.3)',scale=1280:-2",
		"-vsync", "vfr",
		"-frames:v", "20",
		"/tmp/out_%03d.jpg",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("SceneDetectArgs =\n  %v\nwant\n  %v", args, want)
	}
}

func TestSceneDetectArgsDefaults(t *testing.T) {
	// Zero maxFrames / threshold fall back to the package defaults.
	args := SceneDetectArgs("/s.mp4", "/o_%03d.jpg", 0, 0)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "select='gt(scene,0.3)'") {
		t.Errorf("default scene threshold not applied: %q", joined)
	}
	if !strings.Contains(joined, "-frames:v 20") {
		t.Errorf("default max frames not applied: %q", joined)
	}
}

func TestIntervalFrameArgs(t *testing.T) {
	args := IntervalFrameArgs("/tmp/in.mov", "/tmp/f.jpg", 12.5)
	want := []string{
		"-y",
		"-ss", "12.50",
		"-i", "/tmp/in.mov",
		"-frames:v", "1",
		"-vf", "scale=1280:-2",
		"/tmp/f.jpg",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("IntervalFrameArgs =\n  %v\nwant\n  %v", args, want)
	}
}

func TestProbeDurationArgs(t *testing.T) {
	args := ProbeDurationArgs("/tmp/in.mp4")
	want := []string{
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=nw=1:nk=1",
		"/tmp/in.mp4",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("ProbeDurationArgs =\n  %v\nwant\n  %v", args, want)
	}
}

func TestIntervalTimestamps(t *testing.T) {
	// 4 frames over a 100s video → midpoints of equal slices.
	got := IntervalTimestamps(100, 4)
	want := []float64{12.5, 37.5, 62.5, 87.5}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("IntervalTimestamps(100,4) = %v, want %v", got, want)
	}
	// Trivial-duration video → single frame at 0.
	if ts := IntervalTimestamps(0.5, 8); !reflect.DeepEqual(ts, []float64{0}) {
		t.Errorf("IntervalTimestamps(0.5,8) = %v, want [0]", ts)
	}
	if ts := IntervalTimestamps(100, 0); ts != nil {
		t.Errorf("IntervalTimestamps(_,0) = %v, want nil", ts)
	}
}

func TestIntervalFrameCount(t *testing.T) {
	cases := []struct {
		dur  float64
		max  int
		want int
	}{
		{0, 20, 6},     // floor at minSceneFrames
		{40, 20, 6},    // 40/8 = 5 -> floor 6
		{80, 20, 10},   // 80/8 = 10
		{1000, 20, 20}, // clamp to max
		{160, 12, 12},  // clamp to custom max
	}
	for _, tc := range cases {
		if got := IntervalFrameCount(tc.dur, tc.max); got != tc.want {
			t.Errorf("IntervalFrameCount(%v,%d) = %d, want %d", tc.dur, tc.max, got, tc.want)
		}
	}
}

func TestNeedsIntervalFallback(t *testing.T) {
	if !NeedsIntervalFallback(3, 60) {
		t.Error("3 scene frames on a 60s video should trigger fallback")
	}
	if NeedsIntervalFallback(10, 60) {
		t.Error("10 scene frames should NOT trigger fallback")
	}
	if NeedsIntervalFallback(0, 0.5) {
		t.Error("trivial-duration video should NOT trigger fallback")
	}
}
