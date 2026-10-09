package transcode

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// These tests need ffmpeg. They skip rather than fail where it is absent, so a
// contributor without it can still run the suite, and CI installs it.
func runner(t *testing.T) *Runner {
	t.Helper()
	r := New(DefaultConfig())
	if err := r.Available(); err != nil {
		t.Skip("ffmpeg not installed")
	}
	return r
}

// makeVideo generates a fixture rather than committing one. A binary blob in
// the repo is a blob nobody can review, and testsrc gives us any duration and
// size we want.
func makeVideo(t *testing.T, seconds int, size string, withAudio bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "in.mp4")

	args := []string{
		"-y",
		"-f", "lavfi", "-i", "testsrc=duration=" + itoa(seconds) + ":size=" + size + ":rate=15",
	}
	if withAudio {
		args = append(args, "-f", "lavfi", "-i", "sine=frequency=440:duration="+itoa(seconds))
	}
	args = append(args, "-pix_fmt", "yuv420p", "-c:v", "libx264", "-preset", "ultrafast")
	if withAudio {
		args = append(args, "-c:a", "aac", "-shortest")
	}
	args = append(args, path)

	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("could not build fixture: %v\n%s", err, out)
	}
	return path
}

func itoa(n int) string {
	return time.Duration(n).String()[:len(time.Duration(n).String())-2]
}

func TestProbe(t *testing.T) {
	r := runner(t)
	in := makeVideo(t, 3, "640x480", true)

	info, err := r.Probe(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 640 || info.Height != 480 {
		t.Errorf("dimensions = %dx%d, want 640x480", info.Width, info.Height)
	}
	if info.Seconds < 2.5 || info.Seconds > 3.5 {
		t.Errorf("seconds = %f, want about 3", info.Seconds)
	}
	if !info.HasAudio {
		t.Error("audio stream not detected")
	}
}

// A host uploading a photo or a voice note must be told what is wrong, not
// left waiting for a transcode that was never going to work.
func TestProbeRejectsNonVideo(t *testing.T) {
	r := runner(t)

	path := filepath.Join(t.TempDir(), "not-a-video.mp4")
	if err := os.WriteFile(path, []byte("this is plainly not a video"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := r.Probe(context.Background(), path); err == nil {
		t.Fatal("expected an error for a non-video file")
	}
}

func TestVideoDownscales(t *testing.T) {
	r := runner(t)
	in := makeVideo(t, 2, "1920x1080", true)
	out := filepath.Join(t.TempDir(), "out.mp4")

	info, err := r.Probe(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}

	res, err := r.Video(context.Background(), in, out, info)
	if err != nil {
		t.Fatal(err)
	}
	if res.Height > 480 {
		t.Errorf("height = %d, want at most 480", res.Height)
	}
	if res.Width%2 != 0 || res.Height%2 != 0 {
		t.Errorf("dimensions %dx%d are not both even; H.264 rejects odd ones", res.Width, res.Height)
	}

	stat, err := os.Stat(out)
	if err != nil || stat.Size() == 0 {
		t.Fatalf("no output written: %v", err)
	}
}

// Without faststart the moov atom sits at the end and a browser downloads the
// whole file before showing a frame. On mobile data that is the difference
// between a two-second start and a twenty-second one, so it is asserted rather
// than assumed.
func TestVideoIsFaststart(t *testing.T) {
	r := runner(t)
	in := makeVideo(t, 2, "640x480", false)
	out := filepath.Join(t.TempDir(), "out.mp4")

	info, _ := r.Probe(context.Background(), in)
	if _, err := r.Video(context.Background(), in, out, info); err != nil {
		t.Fatal(err)
	}

	head := make([]byte, 4096)
	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	n, _ := f.Read(head)

	if !contains(head[:n], []byte("moov")) {
		t.Error("moov atom is not in the first 4KB; faststart did not apply")
	}
}

func TestVideoTruncatesToMaxSeconds(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxSeconds = 2

	r := New(cfg)
	if err := r.Available(); err != nil {
		t.Skip("ffmpeg not installed")
	}

	in := makeVideo(t, 6, "640x480", false)
	out := filepath.Join(t.TempDir(), "out.mp4")

	info, _ := r.Probe(context.Background(), in)
	res, err := r.Video(context.Background(), in, out, info)
	if err != nil {
		t.Fatal(err)
	}
	if res.Seconds > 3 {
		t.Errorf("seconds = %d, want truncated to about 2", res.Seconds)
	}
}

// A silent clip is perfectly normal and must not fail the encode.
func TestVideoWithoutAudio(t *testing.T) {
	r := runner(t)
	in := makeVideo(t, 2, "640x480", false)
	out := filepath.Join(t.TempDir(), "out.mp4")

	info, err := r.Probe(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if info.HasAudio {
		t.Fatal("fixture should have no audio")
	}
	if _, err := r.Video(context.Background(), in, out, info); err != nil {
		t.Fatalf("a silent video must transcode: %v", err)
	}
}

func TestPoster(t *testing.T) {
	r := runner(t)
	in := makeVideo(t, 3, "1280x720", false)
	out := filepath.Join(t.TempDir(), "poster.jpg")

	info, _ := r.Probe(context.Background(), in)
	if err := r.Poster(context.Background(), in, out, info); err != nil {
		t.Fatal(err)
	}

	stat, err := os.Stat(out)
	if err != nil || stat.Size() == 0 {
		t.Fatalf("no poster written: %v", err)
	}
}

// Clips shorter than the seek point have no frame at one second, so the seek
// falls back to zero rather than producing an empty file.
func TestPosterOnVeryShortVideo(t *testing.T) {
	r := runner(t)
	in := makeVideo(t, 1, "640x480", false)
	out := filepath.Join(t.TempDir(), "poster.jpg")

	info, _ := r.Probe(context.Background(), in)
	if err := r.Poster(context.Background(), in, out, info); err != nil {
		t.Fatal(err)
	}
	if stat, err := os.Stat(out); err != nil || stat.Size() == 0 {
		t.Fatalf("no poster from a one-second clip: %v", err)
	}
}

func TestRejectsAbsurdlyLongInput(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxSeconds = 2

	r := New(cfg)
	if err := r.Available(); err != nil {
		t.Skip("ffmpeg not installed")
	}

	// Past a generous multiple of the cap, decoding the whole file to keep two
	// seconds of it is wasted worker time.
	_, err := r.Video(context.Background(), "unused", "unused", Info{Seconds: 600})
	if !errors.Is(err, ErrTooLong) {
		t.Errorf("err = %v, want ErrTooLong", err)
	}
}

func TestMissingBinary(t *testing.T) {
	cfg := DefaultConfig()
	cfg.FFmpegPath = "ffmpeg-that-does-not-exist"

	if err := New(cfg).Available(); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func contains(haystack, needle []byte) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
