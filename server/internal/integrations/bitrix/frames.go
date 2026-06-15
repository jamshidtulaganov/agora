package bitrix

import (
	"path"
	"strconv"
	"strings"
)

// Video frame extraction for Bitrix attachments. Agents can't "watch" a bug
// recording, so a video attachment is decomposed into a handful of still frames
// (uploaded as image attachments) that the Planner's claim brief can carry. The
// pure argument builders here are the testable core; the handler shells out to
// ffmpeg/ffprobe with these args (see handler/bitrix_attachments.go).
//
// Mirrors the legacy bot (bots/video.py): scene-change detection as the primary
// strategy with interval sampling as the fallback for mostly-static videos.

// videoExtensions is the set of file extensions (lowercase, with dot) treated
// as video for frame extraction. Matches the legacy bot's VIDEO_EXT.
var videoExtensions = map[string]bool{
	".mp4":  true,
	".mov":  true,
	".avi":  true,
	".mkv":  true,
	".webm": true,
	".m4v":  true,
	".wmv":  true,
	".flv":  true,
}

// DefaultSceneThreshold is the ffmpeg scene-change score above which a frame is
// captured. 0.30 matches the legacy bot — high enough to skip near-identical
// frames, low enough to catch clicks / page transitions / errors.
const DefaultSceneThreshold = 0.30

// MaxVideoFrames bounds how many frames a single video yields, so a long
// recording can't fan out into hundreds of image attachments.
const MaxVideoFrames = 20

// minSceneFrames is the floor below which scene detection is considered to have
// found too few cuts (a mostly-static video), triggering the interval fallback.
const minSceneFrames = 6

// IsVideo reports whether a filename or content type denotes a video that
// should be decomposed into frames. A content type with the "video/" prefix
// wins; otherwise the extension is consulted.
func IsVideo(filename, contentType string) bool {
	if ct := strings.ToLower(strings.TrimSpace(contentType)); strings.HasPrefix(ct, "video/") {
		return true
	}
	return videoExtensions[strings.ToLower(path.Ext(filename))]
}

// SceneDetectArgs builds the ffmpeg arguments for the primary scene-change
// extraction pass. outPattern is a printf-style path like ".../frame_%03d.jpg".
// It mirrors the legacy bot exactly:
//
//	ffmpeg -y -i <src> -vf "select='gt(scene,<t>)',scale=1280:-2" -vsync vfr -frames:v <max> <out>
//
// The returned slice is the args AFTER the program name (ready for exec.Command).
func SceneDetectArgs(src, outPattern string, maxFrames int, sceneThreshold float64) []string {
	if maxFrames <= 0 {
		maxFrames = MaxVideoFrames
	}
	if sceneThreshold <= 0 {
		sceneThreshold = DefaultSceneThreshold
	}
	vf := "select='gt(scene," + strconv.FormatFloat(sceneThreshold, 'f', -1, 64) + ")',scale=1280:-2"
	return []string{
		"-y",
		"-i", src,
		"-vf", vf,
		"-vsync", "vfr",
		"-frames:v", strconv.Itoa(maxFrames),
		outPattern,
	}
}

// IntervalFrameArgs builds the ffmpeg arguments for ONE evenly-spaced still at
// timestamp ts (seconds), used by the interval fallback for static videos:
//
//	ffmpeg -y -ss <ts> -i <src> -frames:v 1 -vf scale=1280:-2 <out>
//
// -ss is placed before -i (input seeking) for speed, matching the legacy bot.
func IntervalFrameArgs(src, outPath string, ts float64) []string {
	return []string{
		"-y",
		"-ss", strconv.FormatFloat(ts, 'f', 2, 64),
		"-i", src,
		"-frames:v", "1",
		"-vf", "scale=1280:-2",
		outPath,
	}
}

// ProbeDurationArgs builds the ffprobe arguments that print a video's duration
// (seconds) to stdout as a bare number:
//
//	ffprobe -v error -show_entries format=duration -of default=nw=1:nk=1 <src>
func ProbeDurationArgs(src string) []string {
	return []string{
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=nw=1:nk=1",
		src,
	}
}

// IntervalTimestamps computes evenly-spaced sample timestamps (seconds) for a
// video of the given duration, returning n timestamps each at the midpoint of
// its slice: dur*(i+0.5)/n. For a video of duration <= 1s it returns a single
// timestamp at 0. Mirrors the legacy bot's _interval_frames spacing.
func IntervalTimestamps(duration float64, n int) []float64 {
	if n <= 0 {
		return nil
	}
	if duration <= 1 {
		return []float64{0}
	}
	out := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, duration*(float64(i)+0.5)/float64(n))
	}
	return out
}

// IntervalFrameCount picks how many interval frames to sample for a static
// video of the given duration: roughly one frame per 8 seconds, clamped to
// [minSceneFrames, maxFrames]. Mirrors the legacy bot's min(max(dur//8,8),max).
func IntervalFrameCount(duration float64, maxFrames int) int {
	if maxFrames <= 0 {
		maxFrames = MaxVideoFrames
	}
	n := int(duration) / 8
	if n < minSceneFrames {
		n = minSceneFrames
	}
	if n > maxFrames {
		n = maxFrames
	}
	return n
}

// NeedsIntervalFallback reports whether the scene-detection pass produced too
// few frames (sceneFrameCount < minSceneFrames) for a non-trivial video
// (duration > 1s), meaning the interval sampler should run to ensure coverage.
func NeedsIntervalFallback(sceneFrameCount int, duration float64) bool {
	return sceneFrameCount < minSceneFrames && duration > 1
}
