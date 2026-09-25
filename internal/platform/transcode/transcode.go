package transcode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"time"
)

var (
	// ErrNotVideo means the file has no decodable video stream. A host who
	// uploaded a voice note or a PDF gets told, rather than getting a generic
	// failure after a long wait.
	ErrNotVideo = errors.New("transcode: no video stream")

	ErrTooLong  = errors.New("transcode: video is longer than allowed")
	ErrNotFound = errors.New("transcode: ffmpeg is not installed")
)

type Config struct {
	FFmpegPath  string
	FFprobePath string

	// Timeout bounds one invocation. ffmpeg on a malformed file can spin for a
	// very long time, and a worker slot held open by a hostile upload is a
	// denial of service with extra steps.
	Timeout time.Duration

	MaxSeconds int
	MaxHeight  int
	CRF        int
	MaxRateK   int
}

func DefaultConfig() Config {
	return Config{
		FFmpegPath:  "ffmpeg",
		FFprobePath: "ffprobe",
		Timeout:     5 * time.Minute,
		MaxSeconds:  60,
		MaxHeight:   480,
		CRF:         28,
		MaxRateK:    900,
	}
}

type Runner struct{ cfg Config }

func New(cfg Config) *Runner {
	if cfg.FFmpegPath == "" {
		cfg = DefaultConfig()
	}
	return &Runner{cfg: cfg}
}

// Available reports whether the binaries exist. The worker checks this at boot
// so a missing ffmpeg is a startup log line rather than a job that fails
// twenty-five times in production.
func (r *Runner) Available() error {
	for _, bin := range []string{r.cfg.FFmpegPath, r.cfg.FFprobePath} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("%w: %s", ErrNotFound, bin)
		}
	}
	return nil
}

type Info struct {
	Seconds  float64
	Width    int
	Height   int
	Codec    string
	HasAudio bool
}

type probeOutput struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		Duration  string `json:"duration"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// Probe reads what a file actually is, before any CPU is spent on it.
//
// Never trust the declared content type. A browser will happily send
// video/mp4 for anything, and the difference between rejecting a file in
// fifteen milliseconds and rejecting it after a twenty-minute transcode is
// the whole reason this runs first.
func (r *Runner) Probe(ctx context.Context, path string) (Info, error) {
	out, err := r.run(ctx, r.cfg.FFprobePath,
		"-v", "error",
		"-show_entries", "stream=codec_type,codec_name,width,height,duration",
		"-show_entries", "format=duration",
		"-of", "json",
		path,
	)
	if err != nil {
		return Info{}, err
	}

	var probed probeOutput
	if err := json.Unmarshal(out, &probed); err != nil {
		return Info{}, fmt.Errorf("transcode: unreadable probe output: %w", err)
	}

	var info Info
	for _, s := range probed.Streams {
		switch s.CodecType {
		case "video":
			if info.Codec != "" {
				continue // first video stream wins
			}
			info.Codec = s.CodecName
			info.Width = s.Width
			info.Height = s.Height
			if d, err := strconv.ParseFloat(s.Duration, 64); err == nil {
				info.Seconds = d
			}
		case "audio":
			info.HasAudio = true
		}
	}

	if info.Codec == "" || info.Width == 0 || info.Height == 0 {
		return Info{}, ErrNotVideo
	}

	// Some containers put the duration only on the format, not the stream.
	if info.Seconds == 0 {
		if d, err := strconv.ParseFloat(probed.Format.Duration, 64); err == nil {
			info.Seconds = d
		}
	}
	return info, nil
}

type Result struct {
	Seconds int
	Width   int
	Height  int
}

// Video transcodes to a single small H.264 file suited to a cheap phone on
// mobile data.
//
// The flags that matter, and why:
//
//	baseline / level 3.1  every Android decoder handles it, including the
//	                      hardware decoders in entry-level Tecno and Infinix
//	yuv420p               phones record 10-bit and odd pixel formats that many
//	                      decoders simply refuse
//	trunc(iw/2)*2         H.264 requires even dimensions; an odd width from a
//	                      rotated clip fails the encode outright
//	+faststart            moves the moov atom to the front, so playback starts
//	                      on the first chunk instead of after the whole file
//	-t MaxSeconds         truncate rather than reject: a host who filmed for
//	                      ninety seconds gets sixty, not an error
//	-an fallback          a silent walkthrough is fine; a missing audio stream
//	                      must not fail the encode
func (r *Runner) Video(ctx context.Context, in, out string, info Info) (Result, error) {
	if r.cfg.MaxSeconds > 0 && info.Seconds > float64(r.cfg.MaxSeconds)*4 {
		// Truncating a ten-minute upload to sixty seconds would still decode
		// the whole thing. Past a generous multiple, reject instead.
		return Result{}, ErrTooLong
	}

	scale := fmt.Sprintf(
		"scale=-2:'min(%d,ih)':force_original_aspect_ratio=decrease,scale=trunc(iw/2)*2:trunc(ih/2)*2",
		r.cfg.MaxHeight)

	args := []string{
		"-y",
		"-protocol_whitelist", "file",
		"-i", in,
		"-vf", scale,
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-crf", strconv.Itoa(r.cfg.CRF),
		"-maxrate", strconv.Itoa(r.cfg.MaxRateK) + "k",
		"-bufsize", strconv.Itoa(r.cfg.MaxRateK*2) + "k",
		"-profile:v", "baseline",
		"-level", "3.1",
		"-pix_fmt", "yuv420p",
		"-movflags", "+faststart",
		"-t", strconv.Itoa(r.cfg.MaxSeconds),
	}

	if info.HasAudio {
		args = append(args, "-c:a", "aac", "-b:a", "64k", "-ac", "1")
	} else {
		args = append(args, "-an")
	}

	args = append(args, out)

	if _, err := r.run(ctx, r.cfg.FFmpegPath, args...); err != nil {
		return Result{}, err
	}

	final, err := r.Probe(ctx, out)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Seconds: int(final.Seconds + 0.5),
		Width:   final.Width,
		Height:  final.Height,
	}, nil
}

// Poster extracts a still for the card and the video element's poster
// attribute, so a listing shows something before a byte of video loads.
//
// One second in, not zero: the first frame of a handheld clip is usually a
// blurred pan or the inside of a pocket. Clips shorter than that fall back to
// the first frame, which is all they have.
func (r *Runner) Poster(ctx context.Context, in, out string, info Info) error {
	at := "1"
	if info.Seconds < 1.5 {
		at = "0"
	}

	_, err := r.run(ctx, r.cfg.FFmpegPath,
		"-y",
		"-protocol_whitelist", "file",
		"-ss", at,
		"-i", in,
		"-frames:v", "1",
		"-vf", "scale=-2:'min(720,ih)'",
		"-q:v", "3",
		out,
	)
	return err
}

func (r *Runner) run(ctx context.Context, bin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// ffmpeg reads some settings from the environment and can be steered by
	// them. An empty environment removes that surface, and nothing here needs
	// one.
	cmd.Env = []string{}

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("transcode: %s timed out after %s", bin, r.cfg.Timeout)
		}
		// ffmpeg's real diagnosis is always on stderr; the exit code alone
		// says nothing useful.
		msg := stderr.String()
		if len(msg) > 2000 {
			msg = msg[len(msg)-2000:]
		}
		return nil, fmt.Errorf("transcode: %s failed: %s", bin, msg)
	}
	return stdout.Bytes(), nil
}
